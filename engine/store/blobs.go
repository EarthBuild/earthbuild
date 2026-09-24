package store

import (
	"sync"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Blobs answers whether the store holds a layer, asking the index first.
//
// The 𝔅 port as the engine actually consults it: `core.Lookup` verifies every
// L2 hit against this, during scheduling, before any sandbox has booted. Today
// it is a stat on a directory the host can see. It cannot be, once the store is
// a disk the guest owns - so the question moves to the index, and this is where
// it moves (E542).
//
// **Both, for now, and that is not indecision.** The index is asked first and
// the store is asked only when the index says no. Where they differ the store
// wins, the gap is closed, and somebody is told:
//
//	index yes            -> yes, and the store is never read
//	index no, store no   -> no
//	index no, store yes  -> yes, note it, report it
//
// The third row is the whole point. It can only mean a layer was filed by a path
// that did not go through Publish, and while the store is still readable that is
// a fact this can observe rather than a theory somebody has to trust. When the
// store stops being readable the fallback simply is not there, and by then the
// third row will have been empty for a long time or the disk is not ready.
//
// Never slower than the stat it replaces: a hit costs one stat instead of one
// stat, and a miss costs two only in the case that used to be a wrong answer
// waiting to happen.
type Blobs struct {
	// Gap is told about a layer the store holds and the index did not.
	//
	// Optional, and a build with no reporter still closes the gap - I11 is
	// degrade *and say so*, and the saying is the caller's to arrange.
	Gap func(id ir.NodeID)

	// said stops one lagging layer being reported by every step that wants it.
	said *sync.Map

	index  Index
	layers LayerStore
}

// OpenBlobs prepares the layer question for a store root.
//
// Opening the index is what migrates a store that predates it, so this is also
// the moment an existing machine keeps its cache (E544).
func OpenBlobs(root string) (Blobs, error) {
	index, err := OpenIndex(root)
	if err != nil {
		return Blobs{}, err
	}

	return Blobs{index: index, layers: LayerStore(root), said: &sync.Map{}}, nil
}

// Has reports whether the layer is here.
//
// **The store answers; the index is checked against it.** Asking the index first
// and returning on its word was the whole of this function, and it inverted the
// invariant Index is built on: the index may lag the store, never lead it. It
// leads the moment anything removes a layer without saying so - a collector, a
// half-finished copy, a user with `rm` - and then this reports a layer that is
// not there, a step is taken as cached, and the build fails materialising a base
// it was promised. Permanently, for that build, until an input changes (E572,
// E573).
//
// **Except where the store cannot be read at all**, which is the other half of
// the rule and the reason this is not simply a stat. Once the store is a disk
// only the guest mounts, a host asking this question has no store to consult and
// the index is the whole of the answer - trustworthy there precisely because
// nothing outside the guest can edit the disk behind it. Phase 2's argument for
// dropping the stat is sound and is about that world.
//
// So the authority is whichever one exists: the store when it is there, the
// index when it is not. Both readings of this function are correct in their own
// world, and the mistake was letting the later world's answer be given in this
// one.
func (b Blobs) Has(id ir.NodeID) bool {
	if !b.layers.Has(id) {
		// A layer that is not there, or a store that is not here. Distinguished
		// only on this path, which a build reaches rarely, so the common answer
		// still costs one stat.
		if !b.layers.Readable() {
			return b.index.Has(id)
		}

		// The store is readable and does not have it, so anything the index says
		// otherwise is the index leading. The repair belongs here: a
		// disagreement found and left alone is one every later build pays to
		// rediscover.
		if b.index.Has(id) {
			_ = b.index.Forget(id)
		}

		return false
	}

	if b.index.Has(id) {
		// Read, so it is not a candidate for collection yet. See Index.Touch.
		b.index.Touch(id)

		return true
	}

	// The store has it and the index did not. Close the gap first: a build that
	// reports the same lag at every step reports nothing anybody reads.
	_ = b.index.Note(id)

	if b.Gap != nil {
		if _, again := b.said.LoadOrStore(id, true); !again {
			b.Gap(id)
		}
	}

	return true
}

// Index is the record this checks against the store, for a caller that needs it
// directly.
func (b Blobs) Index() Index { return b.index }
