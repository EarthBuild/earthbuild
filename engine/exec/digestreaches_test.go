package exec

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/internal/sourceguard"
)

// Both backends hand the guest the digest function the host is using.
//
// **The one setting whose absence is not merely silent.** A guest's environment
// comes from its kernel command line rather than from the process that started
// the machine, so a variable missing from a backend's list is ignored inside the
// VM - which the list's own comment records as having made fifteen settings look
// like they had no effect.
//
// For ℋ the consequence is worse than no effect. The guest hashes the layers it
// captures and the host keys on what it is told, so a guest left on BLAKE3 while
// the host was moved to SHA-256 files every layer under a name the host will
// never derive, and every digest crossing the boundary is a claim about a
// function the other end is not using. The build does not fail; it stops hitting.
//
// Checked in the source because one backend builds a list and the other appends
// arguments inline, and the property is "this name appears in what each backend
// passes" either way.
func TestBothBackendsPassTheDigestFunction(t *testing.T) {
	t.Parallel()

	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this package")
	}

	dir := filepath.Dir(here)

	found, err := sourceguard.NonTestFilesContaining(dir, "ir.EnvDigest")
	if err != nil {
		t.Fatal(err)
	}

	for _, backend := range []string{"usernet_linux.go", "apple_darwin.go"} {
		if found[backend] == 0 {
			t.Errorf("%s does not pass %s to the guest"+
				"\n  the host and the guest would hash with different functions,"+
				"\n  so the guest files layers under names the host cannot derive"+
				"\n  and the build quietly stops hitting the cache",
				backend, ir.EnvDigest)
		}
	}

	if len(found) < 2 {
		t.Errorf("only %d file(s) mention it: %v", len(found), keysOf(found))
	}
}

func keysOf(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}

// And the Linux backend's list really contains it, not merely the file.
func TestTheLinuxGuestSettingsCarryTheDigestFunction(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the settings list is the Linux backend's; the source guard beside" +
			" this is what holds the property elsewhere")
	}

	// Not parallel: t.Setenv forbids it, and the environment is process-wide.
	t.Setenv(ir.EnvDigest, "sha256")

	var carried bool

	for _, kv := range guestSettingsForTest() {
		if strings.HasPrefix(kv, ir.EnvDigest+"=") {
			carried = true
		}
	}

	if !carried {
		t.Errorf("%s is set and the guest is not told", ir.EnvDigest)
	}
}
