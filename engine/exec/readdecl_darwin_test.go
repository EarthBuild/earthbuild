//go:build darwin

package exec

import "testing"

// The backend whose store the host cannot open must hand over both halves of a
// stack element: its layers, and what it declares.
//
// It had the first and not the second, so an image it wrote carried no
// environment - the same defect the microVM had, found by looking for the same
// shape rather than by anyone hitting it on a Mac.
func TestTheAppleBackendCanHandOverBothHalvesOfAnElement(t *testing.T) {
	t.Parallel()

	var sb Sandbox = &Apple{}

	if _, ok := sb.(LayerPacker); !ok {
		t.Error("the Apple backend cannot pack its own layers")
	}

	if _, ok := sb.(DeclarationReader); !ok {
		t.Error("the Apple backend cannot read its own declarations, so an" +
			" image written from it would carry no PATH")
	}
}
