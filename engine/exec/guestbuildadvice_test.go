//go:build darwin

package exec

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The advice for building the guest builds one that runs.
//
// The guest runs *inside* the sandbox, which is Linux whatever the machine is.
// On darwin the message said
//
//	in a checkout: go build -o .../earth-guestd ./cmd/earth-guestd
//
// and following it produces a Mach-O binary, which the VM rejects with
// `Exec format error` - naming neither the cause nor the fix. **Advice that
// cannot be followed successfully is worse than none**, because it is followed
// (E490).
//
// Two things it has to say on darwin and does not need to on Linux: the target
// platform, and that cgo is off - a cross-build with cgo enabled fails to
// compile against the host SDK, which is the *next* thing that happens to
// somebody following it.
func TestTheGuestBuildAdviceCanBeFollowed(t *testing.T) {
	t.Parallel()

	_, err := findGuestBinary()
	if err == nil {
		t.Skip("a guest is installed here, so there is no advice to check")
	}

	got := err.Error()

	// **The `runtime.GOOS != "linux"` these conditions used to carry was dead.**
	// This file is `//go:build darwin`, so it was always true and read as
	// though the assertions were conditional on something. They are not: on a
	// Mac the advice must always name the target platform and always say cgo is
	// off, because following it without either produces a binary the sandbox
	// refuses with `Exec format error`.
	if !strings.Contains(got, "GOOS=linux") {
		t.Errorf("the advice is %q\n  on %s that builds a binary the sandbox"+
			" cannot run", got, runtime.GOOS)
	}

	if !strings.Contains(got, "CGO_ENABLED=0") {
		t.Errorf("the advice is %q\n  a cross-build with cgo on does not"+
			" compile, which is the next thing that happens to whoever follows"+
			" it", got)
	}
}

// A guest that is not an ELF binary at all is named as such.
//
// `checkGuestArch` compares the architecture of an ELF file against the sandbox's.
// Handed something that is not an ELF - a Mach-O, from following the advice
// above before it was fixed - it returned nil and let exec decide, and exec
// decided from inside the VM: `failed to exec [/earth/earth-guestd] Exec format
// error`, twice, followed by a handshake timeout.
//
// **The check knew and said nothing.** A file that is not an executable this
// sandbox can run is exactly what it exists to catch (E490).
func TestAGuestThatIsNotAnELFIsRefusedHere(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "darwin" {
		t.Skip("checkGuestArch is the darwin sandbox's check")
	}

	path := filepath.Join(t.TempDir(), "earth-guestd")

	// A Mach-O header, which is what `go build` produces on this machine.
	err := os.WriteFile(path, []byte{0xcf, 0xfa, 0xed, 0xfe, 0x0c, 0, 0, 1}, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = checkGuestArch(path, "arm64")
	if err == nil {
		t.Fatal("a Mach-O binary was passed to a Linux sandbox without" +
			" comment, so the failure arrives from inside the VM")
	}

	for _, want := range []string{"earth-guestd", "GOOS=linux"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refused with %q, which does not say %q", err, want)
		}
	}
}
