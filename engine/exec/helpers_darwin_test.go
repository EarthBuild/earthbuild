//go:build darwin

package exec_test

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"
)

// buildGuestd compiles earth-guestd for the sandbox's architecture.
//
// Production deliberately does not do this - a shipped binary cannot compile
// itself from a user's project directory, which has no go.mod - so providing it
// is the test's job. EARTH_GUESTD overrides, for containers with no toolchain.
func buildGuestd(t *testing.T) string {
	t.Helper()

	if p := os.Getenv("EARTH_GUESTD"); p != "" {
		return p
	}

	_, err := osexec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain and EARTH_GUESTD is unset")
	}

	out := filepath.Join(t.TempDir(), "earth-guestd")

	build := osexec.Command("go", "build", "-o", out,
		"github.com/EarthBuild/earthbuild/cmd/earth-guestd")
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+probeArch(), "CGO_ENABLED=0")

	msg, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("build earth-guestd: %v: %s", err, msg)
	}

	return out
}
