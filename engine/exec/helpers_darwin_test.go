//go:build darwin

package exec_test

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// buildGuestd compiles earth-guestd for the sandbox's architecture.
//
// Production deliberately does not do this - a shipped binary cannot compile
// itself from a user's project directory, which has no go.mod - so providing it
// is the test's job. EARTH_GUESTD overrides, for containers with no toolchain.
//
// **Built once for the package.** Eight tests asked for it and each got its own
// cross-compile into its own `t.TempDir()`, at 1.1 to 1.9 seconds a time - a
// third of this package's runtime spent producing the same bytes eight times.
// The output is a function of the source and two environment variables, so the
// copies were identical by construction; nothing writes to the binary, so one
// copy is as safe to share as eight were.
func buildGuestd(t *testing.T) string {
	t.Helper()

	if p := os.Getenv("EARTH_GUESTD"); p != "" {
		return p
	}

	guestdOnce.Do(compileGuestd)

	if guestdSkip != "" {
		t.Skip(guestdSkip)
	}

	if errGuestd != nil {
		t.Fatal(errGuestd)
	}

	return guestdPath
}

var (
	guestdOnce sync.Once
	guestdPath string
	guestdSkip string
	errGuestd  error
)

// compileGuestd cross-compiles the agent into a directory TestMain removes.
//
// Not `t.TempDir()`, which belongs to whichever test happened to be first and
// is removed when that test ends - leaving every later test pointed at a path
// that is no longer there. The directory outlives them all and is cleaned up
// once, which is the same arrangement the interp corpus copy uses.
func compileGuestd() {
	_, err := osexec.LookPath("go")
	if err != nil {
		guestdSkip = "no go toolchain and EARTH_GUESTD is unset"

		return
	}

	dir, err := os.MkdirTemp("", "guestd")
	if err != nil {
		errGuestd = fmt.Errorf("make a directory for earth-guestd: %w", err)

		return
	}

	keepUntilTheEnd(dir)

	out := filepath.Join(dir, "earth-guestd")

	// Background: this outlives any one test by design, so there is no test's
	// context to inherit and cancelling it would strand the tests that follow.
	build := osexec.CommandContext(context.Background(), "go", "build", "-o", out,
		"github.com/EarthBuild/earthbuild/cmd/earth-guestd")
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+probeArch(), "CGO_ENABLED=0")

	msg, buildErr := build.CombinedOutput()
	if buildErr != nil {
		errGuestd = fmt.Errorf("build earth-guestd: %w: %s", buildErr, msg)

		return
	}

	guestdPath = out
}
