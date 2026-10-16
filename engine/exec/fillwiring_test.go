//go:build linux

package exec

import "testing"

// A sandbox with no filler gives the guest no fault-in channel.
//
// Every build today, and the property that makes the unfinished path safe to
// have landed: absent a filler, nothing is passed, the guest configures nothing,
// its tracer fills nothing, and the capture excludes nothing (E297).
//
// Asserted on the field rather than on the behaviour because the behaviour needs
// a booted sandbox - and what is being checked is that the default is *off*,
// which a zero value either is or is not.
func TestASandboxWithNoFillerIsTheDefault(t *testing.T) {
	t.Parallel()

	if (&Native{}).Fill != nil {
		t.Error("a sandbox offered to fault paths in without being asked")
	}
}
