//go:build linux

package exec

import "testing"

// The backend whose store the host cannot read must be able to hand layers over.
//
// A compile-time assertion would catch this too, and this is the same check
// written so that its failure says what was lost: without it `SAVE IMAGE` still
// succeeds, and writes an image with no filesystem in it.
func TestTheMicroVMCanHandItsLayersToTheHost(t *testing.T) {
	t.Parallel()

	var sb Sandbox = &Firecracker{}

	if _, ok := sb.(LayerPacker); !ok {
		t.Error("the microVM backend cannot pack its own layers, so an image" +
			" written from it would have none")
	}
}

// And the namespace backend does not need to: its store is a directory this
// process reads directly, so the packing branch must not be taken for it.
func TestTheNamespaceBackendNeedsNoPacker(t *testing.T) {
	t.Parallel()

	var sb Sandbox = &Native{}

	if _, ok := sb.(LayerPacker); ok {
		t.Error("the namespace backend claims to pack layers; the host reads" +
			" its store itself, and two routes to one blob can disagree")
	}
}

// The backend whose store the host cannot read must also hand over what a stack
// element declares, or the image it writes inherits no environment.
func TestTheMicroVMCanHandOverWhatAnElementDeclares(t *testing.T) {
	t.Parallel()

	var sb Sandbox = &Firecracker{}

	if _, ok := sb.(DeclarationReader); !ok {
		t.Error("the microVM backend cannot read its own declarations, so an" +
			" image written from it would carry no PATH")
	}
}
