package core

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A variant is not an architecture, and qemu does not have one.
//
// The kernel registers `qemu-arm`, which this engine reads as `linux/arm` -
// there is no variant to read, and the interpreter runs v5, v6 and v7 binaries
// alike. A step's platform does have one: `tests/platform` builds for
// `linux/arm/v7`, and placement compared the two whole, so a machine with qemu
// registered for arm was not eligible to emulate arm.
//
// The diagnosis said so without saying so: `no eligible worker: this step is for
// linux/arm/v7 and this build has linux/amd64`, which is what a machine with no
// emulation at all says too - so the message could not distinguish an
// unregistered interpreter from a registered one that placement would not use
// (E942).
//
// `checkRunnableWith` had the rule already and states it in as many words; this
// is the same comparison, in the other place that makes it.
func TestEmulationIgnoresTheVariant(t *testing.T) {
	t.Parallel()

	arm := Worker{Emulates: []ir.Platform{{OS: "linux", Arch: "arm"}}}

	for _, tc := range []struct {
		name string
		want ir.Platform
		ok   bool
	}{{
		name: "the architecture with no variant",
		want: ir.Platform{OS: "linux", Arch: "arm"},
		ok:   true,
	}, {
		name: "the same architecture with a variant",
		want: ir.Platform{OS: "linux", Arch: "arm", Variant: "v7"},
		ok:   true,
	}, {
		name: "another variant of it",
		want: ir.Platform{OS: "linux", Arch: "arm", Variant: "v6"},
		ok:   true,
	}, {
		name: "a different architecture",
		want: ir.Platform{OS: "linux", Arch: "arm64"},
	}, {
		name: "the same architecture on another OS",
		want: ir.Platform{OS: "darwin", Arch: "arm"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := arm.canEmulate(tc.want); got != tc.ok {
				t.Errorf("a machine emulating linux/arm reports %v for %s, want %v",
					got, tc.want, tc.ok)
			}
		})
	}

	// A machine that emulates nothing emulates nothing.
	if (Worker{}).canEmulate(ir.Platform{OS: "linux", Arch: "arm", Variant: "v7"}) {
		t.Error("a machine with no interpreter registered claims to emulate one")
	}
}
