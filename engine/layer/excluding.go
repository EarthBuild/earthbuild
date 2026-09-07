package layer

import (
	"fmt"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TakeExcluding captures a tree, leaving out files this engine put there.
//
// **The way round the obstacle in a lazy base.** A step's delta is where its
// writes land and its base is what it reads; overlayfs keeps them apart with two
// directories, and a lazy base cannot use that - a lowerdir may not change under
// a live mount, and a fault-in is exactly a change to the base while the step
// runs (E293).
//
// So a faulted-in file lands in the upper directory with the step's own writes,
// and the capture leaves it out. The engine knows precisely what it put there,
// which is what makes this exact rather than a heuristic.
//
// **By name and by digest, both.** A step may read a file from its base and then
// write it - `sed -i` over a config, a compiler updating a cache - and that file
// is genuinely part of the delta. Dropping it by name alone would lose a real
// write; dropping it only when it is still exactly what this engine placed is
// the difference between a correct layer and a quietly incomplete one.
//
// A lazily materialised step must produce **the same layer** an eagerly
// materialised one produces, or the cache is a lottery (I1).
func TakeExcluding(root string, faulted map[string]ir.NodeID) (Capture, error) {
	return TakeExcludingIn(root, faulted, IDMap{}, IDMap{})
}

// TakeExcludingIn is TakeExcluding with ownership translated as TakeIn does.
func TakeExcludingIn(
	root string, faulted map[string]ir.NodeID, uids, gids IDMap,
) (Capture, error) {
	if len(faulted) == 0 {
		return TakeIn(root, uids, gids)
	}

	entries, size, err := walk(root)
	if err != nil {
		return Capture{}, err
	}

	kept := entries[:0]
	seen := make(map[string]bool, len(faulted))

	for _, e := range entries {
		seen[e.path] = true

		if was, ok := faulted[e.path]; ok && placedStill(e, was) {
			// Still exactly what this engine placed, so it is base and not
			// delta.
			size -= e.size

			continue
		}

		kept = append(kept, e)
	}

	// **A file the engine placed and the step removed.**
	//
	// In an overlay that is a whiteout in the upper directory, and the layer
	// says "this is gone". A lazy base has no overlay (E293): the file is simply
	// absent, this capture sees nothing where something used to be, and the
	// layer would say **nothing at all** - so materialising base plus delta
	// still shows the file. The step succeeded, the layer is real, and it means
	// something different from what happened.
	//
	// Refused rather than recorded, because recording it needs a whiteout this
	// engine cannot make here: the marker is a character device, which wants
	// CAP_MKNOD, and inventing a different marker would be a second deletion
	// convention (I10, E294).
	for path := range faulted {
		if !seen[path] {
			return Capture{}, fmt.Errorf("%w: %s was materialised for this step"+
				" and is gone"+
				"\n  a lazily materialised base cannot record a deletion, and a"+
				" layer that omits one means something the step did not do",
				ErrMalformed, path)
		}
	}

	return capture(kept, size, uids, gids), nil
}

// placedStill reports whether an entry is still exactly what the engine put
// there.
//
// A **file** matches by content, so a step that edited it keeps it (E293). A
// **directory** matches by being one: it was made to hold a placed file, and in
// an overlay it would not exist in the delta at all, because reading a base file
// creates nothing in the upper (E306). A zero digest is how the caller says "a
// directory I made".
//
// A step that makes the same directory itself loses nothing. The base already
// has it - which is why the engine made it - so the delta need not record it.
func placedStill(e entry, was ir.NodeID) bool {
	switch kindOf(e.mode) {
	case 'f':
		return e.content == was
	case 'd':
		return was == ir.NodeID{}
	}

	return false
}

// ContentID is the digest a capture records for a file's contents.
//
// Exported so that whoever faults a file in can say what it put there, in the
// terms the capture will compare against. Two spellings of "the digest of these
// bytes" would be two things to get out of step.
func ContentID(body []byte) ir.NodeID {
	h := ir.NewHasher()
	h.Fixed(body)

	return h.Sum()
}
