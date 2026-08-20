//go:build darwin

package interp_test

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"
)

// guestd compiles the sandbox agent for these tests.
//
// The engine deliberately does not build it at run time - a shipped binary
// cannot compile itself from a user's project directory - so tests provide it.
func guestd(t *testing.T) string {
	t.Helper()

	if p := os.Getenv("EARTH_GUESTD"); p != "" {
		return p
	}

	out := filepath.Join(t.TempDir(), "earth-guestd")

	build := osexec.Command("go", "build", "-o", out,
		"github.com/EarthBuild/earthbuild/cmd/earth-guestd")
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0")

	msg, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("build earth-guestd: %v: %s", err, msg)
	}

	return out
}
