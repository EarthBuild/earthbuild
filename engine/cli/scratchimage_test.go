package cli

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestAnImageFromScratchMayHaveNoLayers.
//
// `FROM scratch` / `WORKDIR /` / `SAVE IMAGE bare` is `tests/scratch-test.earth`
// in full, and it produces an image with nothing in it - which is the point of
// scratch, and how a from-nothing base image is made.
//
// An empty stack was read as "the step producing it did not run", because the
// two look identical from here: a node that never executed and a node that
// executed and made no layers both have nothing to show. They are not the same
// thing, and only the operation says which - `OpScratch` *is* the empty image,
// so having no layers is the correct answer rather than a missing one.
func TestAnImageFromScratchMayHaveNoLayers(t *testing.T) {
	t.Parallel()

	scratch := &ir.Node{Op: ir.Op{Kind: ir.OpScratch}}
	if !emptyStackIsExpected(scratch) {
		t.Error("an image from scratch with no layers was read as a step that" +
			" did not run; scratch has no layers by definition")
	}

	// Everything else keeps the diagnosis: a step that should have produced
	// layers and produced none did not run, and saying so is the whole value
	// of the check.
	for _, kind := range []ir.OpKind{ir.OpExec, ir.OpImage, ir.OpFile} {
		if emptyStackIsExpected(&ir.Node{Op: ir.Op{Kind: kind}}) {
			t.Errorf("an empty stack from %v was accepted, so a step that never"+
				" ran would be written out as an empty image", kind)
		}
	}
}
