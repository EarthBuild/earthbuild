package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Want is how much room a store should have after collecting, and what the
// caller knows that changes what it costs to lose a layer.
type Want struct {
	// Free is the bytes the store should have once this is done. Collecting
	// stops the moment it is reached: the store is only worth having because
	// the next build reads it, so the default is always to keep.
	Free int64

	// Elsewhere reports whether a layer can be had from somewhere other than
	// this machine - a peer, a registry - so losing it costs a *fetch* rather
	// than a *rebuild*.
	//
	// **A fleet holds more than one machine can**, which makes the two kinds of
	// loss quite different in price and means age alone is the wrong order. A
	// layer nobody else has is the expensive one, however long ago it was read.
	//
	// Nil is "no idea", and then age decides on its own.
	Elsewhere func(ir.NodeID) bool
}

// Report is what a collection did.
type Report struct {
	Dropped int
	Kept    int
}

// Freed is called after each layer is removed, and exists so a test can move
// the free space it is pretending to measure.
type Freed func()

// Collect removes layers until the store has the room asked for.
//
// **Least-recently-read first, and recoverable before irrecoverable.** The index
// records when each layer was last used, so what goes is what builds have
// stopped asking for; and a layer the fleet still holds costs a fetch to lose
// where one only this machine has costs a rebuild. Age alone would treat those
// as the same thing.
//
// **Forgotten before deleted**, which is the index's own rule and not this
// function's preference: a layer the index claims and the store lacks is a cache
// hit against nothing, which is a wrong build reporting success. The other order
// costs a rebuild and nothing else.
//
// A store whose room cannot be measured is left alone and the failure reported:
// collecting on a guess throws away a cache for a number nobody has.
//
// Nothing else may be using the store. There is no lock here because there is no
// safe way to take one that a build would not then have to respect on every
// read - so this runs where nothing is running, which is a guest at start.
func Collect(root string, want Want, free func(string) (int64, error), after ...Freed) (Report, error) {
	have, err := free(root)
	if err != nil {
		return Report{}, fmt.Errorf("measure the room left in %s: %w", root, err)
	}

	if have >= want.Free {
		return Report{}, nil
	}

	idx, err := OpenIndex(root)
	if err != nil {
		return Report{}, err
	}

	order, err := collectable(root, idx, want)
	if err != nil {
		return Report{}, err
	}

	var out Report

	for _, id := range order {
		if have >= want.Free {
			break
		}

		// Forget first. See above: the index may lag the store and must never
		// lead it.
		err = idx.Forget(id)
		if err != nil {
			return out, fmt.Errorf("forget layer %s before removing it: %w", id, err)
		}

		err = os.RemoveAll(LayerStore(root).Path(id))
		if err != nil {
			return out, fmt.Errorf("remove layer %s: %w", id, err)
		}

		out.Dropped++

		for _, f := range after {
			f()
		}

		have, err = free(root)
		if err != nil {
			return out, fmt.Errorf("measure the room left in %s: %w", root, err)
		}
	}

	out.Kept = len(order) - out.Dropped

	return out, nil
}

// collectable is every layer, in the order it should go.
func collectable(root string, idx Index, want Want) ([]ir.NodeID, error) {
	entries, err := os.ReadDir(filepath.Join(root, "layers"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("read the layers in %s: %w", root, err)
	}

	type aged struct {
		id        ir.NodeID
		used      time.Time
		elsewhere bool
	}

	var all []aged

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		id, parseErr := ir.ParseNodeID(e.Name())
		if parseErr != nil {
			// Not a layer. The store holds other things and a collector that
			// deleted what it could not name would be the worst kind.
			continue
		}

		it := aged{id: id, used: idx.Used(id)}
		if want.Elsewhere != nil {
			it.elsewhere = want.Elsewhere(id)
		}

		all = append(all, it)
	}

	sort.SliceStable(all, func(i, j int) bool {
		// Recoverable first, whatever the ages: losing one costs a fetch and
		// losing the other costs a build.
		if all[i].elsewhere != all[j].elsewhere {
			return all[i].elsewhere
		}

		return all[i].used.Before(all[j].used)
	})

	out := make([]ir.NodeID, len(all))
	for i, a := range all {
		out[i] = a.id
	}

	return out, nil
}
