package exec_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// A sandbox that registered Rosetta can run amd64.
//
// **The name is the whole of it.** Apple's `container --rosetta` registers its
// interpreter as `x86_64`, which is the spelling `tonistiigi/binfmt` uses and
// which this engine's table already maps - the reason its comment records
// getting the two spellings wrong twice. So a guest reporting what its kernel
// says needs no new vocabulary, and a Mac gains amd64 builds by being asked the
// right question rather than by learning a new answer.
func TestASandboxWithRosettaRunsAmd64(t *testing.T) {
	t.Parallel()

	got := exec.PlatformsNamed([]string{"x86_64"})
	if len(got) != 1 || got[0].Arch != "amd64" || got[0].OS != "linux" {
		t.Fatalf("a guest reporting x86_64 emulates %v", got)
	}
}

// Both spellings of a qemu registration are understood, and nothing else is.
func TestOnlyRecognisedInterpretersCount(t *testing.T) {
	t.Parallel()

	got := exec.PlatformsNamed([]string{
		"qemu-x86_64", // Debian's qemu-user-binfmt
		"aarch64",     // tonistiigi/binfmt
		"jarwrapper",  // a real entry on a machine with a JVM, and no architecture
	})

	if len(got) != 2 {
		t.Fatalf("read %v from two interpreters and a JVM", got)
	}

	// Sorted, because this reaches placement and two runs must agree (I12).
	if got[0].Arch != "amd64" || got[1].Arch != "arm64" {
		t.Errorf("platforms came back as %v, which is not sorted", got)
	}
}
