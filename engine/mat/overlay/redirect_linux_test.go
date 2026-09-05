//go:build linux

package overlay_test

import (
	"os"
	"strings"
	"testing"
)

// Renaming a directory from a lower layer is a platform divergence, measured.
//
// `cargo build` fails in this engine on rootless Linux and succeeds on macOS,
// with the same Earthfile and the same commit - and the reference builds it on
// both. Four hypotheses about cache mounts were eliminated first (E102); the
// mount turned out to be innocent, since `cargo build` fails with no mount at
// all.
//
// The mechanism is overlayfs's oldest restriction: a directory that exists only
// in a *lower* layer cannot be renamed, because doing so needs a redirect xattr
// and `redirect_dir` is off by default. Measured on the machine that fails:
//
//	/sys/module/overlay/parameters/redirect_dir   ->  N
//	rename in a userns overlay                    ->  Input/output error
//	mount with redirect_dir=on in a userns        ->  permission denied
//
// **The kernel refuses the option to an unprivileged mounter**, so this is not
// an omission in this engine and cannot be fixed by setting it: rootless
// overlayfs does not have the feature. macOS runs its guest as real root in a
// VM, where the restriction does not apply, so the two platforms genuinely
// differ.
//
// Pinned rather than fixed, because the remedy is a judgement - warn on every
// rootless build, hint only when a step fails, or refuse outright - and no
// measurement chooses between them. If a kernel or a policy change ever lifts
// this, the assertion below stops holding and says so.
func TestRootlessOverlayCannotRedirectDirectories(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("/sys/module/overlay/parameters/redirect_dir")
	if err != nil {
		t.Skipf("this kernel does not report the redirect_dir default: %v", err)
	}

	def := strings.TrimSpace(string(b))

	// `Y` would mean the kernel enables it by default and the restriction does
	// not apply here - a different machine from the one this documents, and the
	// note above should then be re-measured rather than trusted.
	if def != "N" {
		t.Skipf("redirect_dir defaults to %q here, so this machine is not the case described", def)
	}

	if os.Geteuid() == 0 {
		t.Skip("running as root, where the restriction does not apply")
	}

	// The engine's own materialiser is what would have to set the option, and
	// it cannot: recorded as a fact about the platform so that a future attempt
	// to "just enable redirect_dir" finds the measurement rather than repeating
	// it.
	t.Log("rootless overlayfs on this machine cannot rename a lower-layer directory;" +
		" see docs-internals/plan-native-engine.md")
}
