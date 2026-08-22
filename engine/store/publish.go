package store

import (
	"fmt"
	"os"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Publish makes a staged tree the layer named by id.
//
// The one moment a layer becomes visible. Three packages arrived at this point
// independently - a transfer from a peer, a step's captured delta, a placement
// on the host - each staging under a unique name inside `layers/` and each
// renaming it into position with its own account of the same race.
//
// Losing that race is success. The id names the content, so whoever got there
// first filed the same bytes and checked them exactly as hard (green paper
// §5.3): the answer to a rename that lands on an existing layer is that the
// layer is there. Stating it once means the next caller cannot state it wrong -
// and it is the seam an index of what the store holds is kept honest at,
// because a layer that becomes visible anywhere becomes visible here (E542).
//
// The staging tree is not removed on failure. Its maker knows what it is and
// every caller already unwinds it; removing it here would be a second owner for
// one directory.
func Publish(root string, id ir.NodeID, staged string) error {
	at := LayerStore(root).Path(id)

	err := os.Rename(staged, at)
	if err != nil {
		// *Failure class: TOCTOU on a check-then-act.* A caller that checked
		// first found the layer absent, and so did the other build doing the
		// same thing. The remedy is not a lock - it is that the loser's work
		// was redundant.
		if !LayerStore(root).Has(id) {
			return fmt.Errorf("file layer %s at %s: %w", id, at, err)
		}
	}

	// After the layer, always. An index entry that arrives first describes a
	// layer that may never exist, and the whole value of the index is that it
	// never claims one (see Index).
	//
	// Noted on the losing path too: the layer is there, whoever filed it.
	return Index(root).Note(id)
}
