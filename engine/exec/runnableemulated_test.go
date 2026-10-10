package exec

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A machine that emulates an architecture can run steps for it.
//
// **Two gates, and only one knew.** The scheduler already places a foreign-
// platform step on a worker whose `Emulates` names it, filled from the kernel's
// binfmt register. The sandbox then refused the same step on a plain platform
// comparison, so a build with qemu registered got past placement and failed at
// execution - with a message saying "nothing emulates one on the other", which
// had just stopped being true (E932).
func TestAnEmulatedPlatformIsRunnable(t *testing.T) {
	t.Parallel()

	arm := []ir.Platform{{OS: "linux", Arch: archARM64}}

	// The case the register exists for.
	err := checkRunnableWith("linux/amd64", "linux/arm64", "Earthfile:1", arm)
	if err != nil {
		t.Errorf("a machine emulating arm64 refused an arm64 step: %v", err)
	}

	// A variant is not an architecture: arm64/v8 is arm64 code.
	err = checkRunnableWith("linux/amd64", "linux/arm64/v8", "Earthfile:1", arm)
	if err != nil {
		t.Errorf("a variant of an emulated architecture was refused: %v", err)
	}

	// And what it does not emulate is still refused, with the advice intact.
	err = checkRunnableWith("linux/amd64", "linux/s390x", "Earthfile:1", arm)
	if err == nil {
		t.Fatal("a platform nothing emulates was allowed")
	}

	if !strings.Contains(err.Error(), "s390x") {
		t.Errorf("the refusal does not name the platform: %v", err)
	}

	// Emulating nothing is the ordinary case and must behave as before.
	err = checkRunnableWith("linux/amd64", "linux/arm64", "Earthfile:1", nil)
	if err == nil {
		t.Error("a machine emulating nothing allowed a foreign step")
	}
}
