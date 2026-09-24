package exec

import (
	"runtime"
	"strings"
	"testing"
)

// The advice for building the guest carries a cross-build prefix exactly where
// one is needed.
//
// The guest runs *inside* the sandbox, which is Linux whatever this machine is.
// On darwin the advice omitted that, and following it produced a Mach-O binary
// the VM rejected with `Exec format error`, naming neither the cause nor the fix
// - advice that cannot be followed successfully is worse than none, because it
// is followed (E490).
//
// On linux the prefix is not merely unnecessary, it is wrong to print: it tells
// somebody to cross-compile for the machine they are already on, which reads as
// though their machine were the problem.
//
// Both halves are asserted here because a mutant deleting the linux shortcut was
// killed on darwin and survived on linux - the existing test covers the platform
// that needs the prefix, and nothing covered the one that does not.
func TestTheGuestBuildAdviceCrossesOnlyWhereItMust(t *testing.T) {
	t.Parallel()

	got := crossPrefix()

	if runtime.GOOS == "linux" {
		if got != "" {
			t.Errorf("on linux the advice carries %q: it tells the reader to"+
				" cross-compile for the machine they are on", got)
		}

		return
	}

	if !strings.Contains(got, "GOOS=linux") {
		t.Errorf("on %s the advice is %q and does not say GOOS=linux: the guest"+
			" runs in the sandbox, which is Linux, and a native build of it is"+
			" rejected with Exec format error", runtime.GOOS, got)
	}

	if !strings.Contains(got, "CGO_ENABLED=0") {
		t.Errorf("on %s the advice is %q and does not disable cgo: a cross"+
			" build with cgo on fails against the host SDK, which is the very"+
			" next thing that happens to whoever follows it", runtime.GOOS, got)
	}

	if !strings.Contains(got, "GOARCH="+runtime.GOARCH) {
		t.Errorf("on %s the advice is %q and does not name this machine's"+
			" architecture", runtime.GOOS, got)
	}
}
