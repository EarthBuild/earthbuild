package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Report is what a collection did.
type Report struct {
	// Before and After are the store's size either side, in bytes.
	Before, After uint64
	// Removed is how many layers went.
	Removed int
	// Kept is how many remain.
	Kept int
	// Reclaimed is what the filesystem gained, measured rather than summed.
	//
	// Set by the free-space path, where nothing is ever sized: statfs reports
	// what a removal actually returned to the disk, including the metadata a
	// sum of file sizes misses. Zero from the ceiling path, which reports
	// through Before and After instead.
	Reclaimed uint64
	// Debris is how many unfinished layer writes were cleared. Counted apart
	// from Removed because they are not layers: nothing could have used them,
	// and losing one costs nothing where losing a layer costs a rebuild.
	Debris int
	// Stopped says the collection gave up its budget before reaching the
	// ceiling, so the store is larger than was asked for and the rest is left
	// for next time. Reported rather than inferred: "freed less than asked"
	// also describes a store with nothing left to give, and those want
	// different words.
	Stopped bool
}

// Freed is how much the collection reclaimed.
func (r Report) Freed() uint64 { return r.Before - r.After }

// String is the one line a person asked for a prune wants back.
func (r Report) String() string {
	// Debris only when there was some. It is the uninteresting case that
	// matters here - a store that keeps reporting cleared debris is a store
	// whose writers keep being killed, and that is worth a reader noticing.
	debris := ""
	if r.Debris > 0 {
		debris = fmt.Sprintf(", cleared %d unfinished write(s)", r.Debris)
	}

	// The measured figure when there is one: a sum of file sizes misses what
	// the directories and metadata cost, and this is the number the disk
	// actually gained.
	freed := r.Freed()
	if r.Reclaimed > 0 {
		freed = r.Reclaimed
	}

	return fmt.Sprintf("removed %d layers%s, freed %s, %d layers and %s left",
		r.Removed, debris, human(freed), r.Kept, human(r.After))
}

// candidate is one layer up for collection, with the two facts that decide its
// fate. Not named `layer`: this package imports a package of that name.
type candidate struct {
	id    ir.NodeID
	bytes uint64
	used  time.Time
}

// Collect removes layers, least recently used first, until the store fits in
// keep bytes.
//
// **Safe because a missing layer is a slow build rather than a wrong one**, and
// that is a recent property rather than an old one: until E573 the index
// answered for the store, so a collected layer was reported present and the
// build that believed it failed for good. With the store asked first, evicting a
// layer the next build wants costs a rebuild - measured at 7.17s for a built
// layer and 7.47s for one re-fetched from a registry - and the artifact is
// unchanged either way.
//
// Least recently *used*, not least recently written. A base image is filed once
// and read by every build afterwards, so writing time would evict exactly the
// layers that cost the most to get back. See Index.Touch.
//
// Sizing every layer means walking the store, which is why this is something a
// person asks for rather than something a build does on its way past.
func Collect(root string, keep uint64) (Report, error) {
	return CollectWith(root, keep, nil)
}

// CollectWith is Collect, told which layers can be had from somewhere else.
//
// **A fleet holds more than one machine can**, so the two ways of losing a layer
// are not the same price: one a peer still has costs a *fetch*, and one nobody
// else has costs a *rebuild*. Least-recently-used alone treats those alike, and
// so takes the expensive one first whenever it happens to be the older - which
// it often is, because a layer only this machine has is usually one this machine
// made.
//
// So `elsewhere` sorts ahead of age: recoverable layers go first whatever their
// age, and only when they run out does the store give up something it cannot get
// back. Nil is "no idea", and then this is exactly Collect as it always was.
func CollectWith(root string, keep uint64, elsewhere func(ir.NodeID) bool) (Report, error) {
	return CollectUntil(root, keep, elsewhere, nil)
}

// CollectUntil is CollectWith, stopping early when stop says so.
//
// **A collector on the critical path can make a machine unusable.** The guest
// agent collects before it serves, so whatever collection costs is spent inside
// the host's handshake budget - and on a store of 44,015 layers with 5G free
// that budget was gone before the agent answered anything. Every sandbox in the
// build then failed with "the guest did not answer the handshake", describing a
// guest that had booted, accepted the connection, and was busy with housekeeping
// nobody was waiting for.
//
// Safe to stop part-way by construction rather than by luck: a layer is
// forgotten from the index before it is deleted, so an interrupted collection
// leaves an index that *lags* - a store holding more than it claims, which is
// the harmless direction. The loop below already said so; nothing was calling
// it in a way that could stop.
//
// A predicate rather than a deadline, so the decision to stop is testable
// without a clock.
//
// nil stop means run to completion, which is every caller but the agent: `earth
// prune` was asked to free space and should finish the job.
func CollectUntil(
	root string, keep uint64, elsewhere func(ir.NodeID) bool, stop func() bool,
) (Report, error) {
	if root == "" {
		return Report{}, nil
	}

	index, err := OpenIndex(root)
	if err != nil {
		return Report{}, err
	}

	// **Before sizing, because debris is space the store does not know it
	// has.** A half-written layer's directory is skipped by `candidates`, so
	// its bytes were neither counted nor reclaimable, and a collector could
	// decide a full store already fit. See sweepPartials.
	debris, freed := sweepPartials(root)

	layers, total, err := candidates(root, index)
	if err != nil {
		return Report{}, err
	}

	report := Report{
		Before: total + freed,
		After:  total,
		Kept:   len(layers),
		Debris: debris,
	}

	// Recoverable first, then oldest use, then by id where two are
	// indistinguishable - so a prune of the same store twice makes the same
	// choices. See CollectWith for why recovery outranks age.
	recoverable := func(id ir.NodeID) bool { return elsewhere != nil && elsewhere(id) }

	sort.Slice(layers, func(i, j int) bool {
		iAway, jAway := recoverable(layers[i].id), recoverable(layers[j].id)
		if iAway != jAway {
			return iAway
		}

		if layers[i].used.Equal(layers[j].used) {
			return layers[i].id.String() < layers[j].id.String()
		}

		return layers[i].used.Before(layers[j].used)
	})

	for _, l := range layers {
		if report.After <= keep {
			break
		}

		if stop != nil && stop() {
			report.Stopped = true

			break
		}

		// **Forget before deleting**, which is Index's own ordering and the
		// reason it holds: an index that lags describes a store that has more
		// than it says, and an index that leads describes layers that are not
		// there. Interrupted here, this store lags.
		_ = index.Forget(l.id)

		err := os.RemoveAll(LayerStore(root).Path(l.id))
		if err != nil {
			return report, fmt.Errorf("collect layer %s: %w", l.id, err)
		}

		report.After -= l.bytes
		report.Removed++
		report.Kept--
	}

	return report, nil
}

// candidates sizes every layer and dates it by last use.
func candidates(root string, index Index) ([]candidate, uint64, error) {
	dir := filepath.Join(root, "layers")

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}

		return nil, 0, fmt.Errorf("read the store's layers: %w", err)
	}

	var (
		layers []candidate
		total  uint64
	)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		id, err := ir.ParseNodeID(e.Name())
		if err != nil {
			// Not a layer. Left alone rather than collected: this removes
			// things, and a name it does not understand is not its business.
			continue
		}

		// SizeAll, not Size: a budgeted walk answers with a floor, and a floor
		// summed into a total is a collector that decides the store already
		// fits and removes nothing (E574).
		size := SizeAll(filepath.Join(dir, e.Name()))
		total += size

		layers = append(layers, candidate{id: id, bytes: size, used: index.Used(id)})
	}

	return layers, total, nil
}
