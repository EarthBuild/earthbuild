//go:build linux

package exec

import "testing"

// A sandbox says whether it can fault paths in.
//
// A method rather than a field, so a caller holding the `Sandbox` interface can
// **ask and be told no**. Not every backend can: the Apple one runs a VM whose
// filesystem this engine does not reach the same way, and a caller that set a
// field it could not see would think it had (E305).
func TestASandboxSaysWhetherItCanFaultPathsIn(t *testing.T) {
	t.Parallel()

	var sb Sandbox = &Native{}

	filler, ok := sb.(interface {
		SetFill(func(handle, path string) error)
	})
	if !ok {
		t.Fatal("the native sandbox does not offer to fault paths in")
	}

	filler.SetFill(func(string, string) error { return nil })

	if sb.(*Native).Fill == nil { //nolint:forcetypeassert // just asserted
		t.Error("it said yes and did nothing")
	}
}
