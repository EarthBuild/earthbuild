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
}

// Freed is how much the collection reclaimed.
func (r Report) Freed() uint64 { return r.Before - r.After }

// String is the one line a person asked for a prune wants back.
func (r Report) String() string {
	return fmt.Sprintf("removed %d layers, freed %s, %d layers and %s left",
		r.Removed, human(r.Freed()), r.Kept, human(r.After))
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
	if root == "" {
		return Report{}, nil
	}

	index, err := OpenIndex(root)
	if err != nil {
		return Report{}, err
	}

	layers, total, err := candidates(root, index)
	if err != nil {
		return Report{}, err
	}

	report := Report{Before: total, After: total, Kept: len(layers)}

	// Oldest use first, and by id where two are indistinguishable, so a prune of
	// the same store twice makes the same choices.
	sort.Slice(layers, func(i, j int) bool {
		if layers[i].used.Equal(layers[j].used) {
			return layers[i].id.String() < layers[j].id.String()
		}

		return layers[i].used.Before(layers[j].used)
	})

	for _, l := range layers {
		if report.After <= keep {
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
