package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
	"github.com/EarthBuild/earthbuild/engine/guest"
)

// TestAStepsProcIsItsOwn.
//
// **A step's `/proc` has to describe the step.** It runs in a PID namespace of
// its own - `isolate` clones with `CLONE_NEWPID`, so its shell is pid 1 - while
// `/proc` is mounted by the guest before that clone and so describes the guest's
// namespace. The step then reports `$$` as 1 and `/proc/self/status` as
// something else entirely, and anything reading `/proc/$$` lands on a different
// process (E705).
//
// The reference engine is self-consistent here. `/proc` has to be mounted from
// inside the namespace that will read it, which is what every container runtime
// does and what the daemon shim already does for its own `/run`.
//
// Asserted on the step's own two accounts of itself rather than on a number:
// which pid it gets is not the engine's business, and agreeing with itself is.
//
// Not parallel: boots a VM, see e2e_sandbox_test.go.
func TestAStepsProcIsItsOwn(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	sh := testShell

	dir := project(t, `VERSION 0.8

t:
    FROM alpine:3.22
    RUN `+sh+` -c 'echo "shell=$$" > /out.txt; grep -E "^Pid:" /proc/self/status >> /out.txt'
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`, nil)

	// **The shim is what this tests, so this turns it on.** It is off by
	// default while it earns its place, and a test that asserted the default
	// would be asserting the bug.
	t.Setenv(guest.EnvStepShim, "1")
	t.Setenv("EARTH_GUESTD", buildGuestd(t))
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, storeDir(t))

	var out bytes.Buffer

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: "t", Out: &out, Platform: testPlatform(),
	})
	if err != nil {
		if strings.Contains(err.Error(), "429") {
			t.Skipf("docker hub rate limit: %v", err)
		}

		t.Fatalf("%v\n%s", err, out.String())
	}

	got, err := os.ReadFile(filepath.Join(dir, testArtefact))
	if err != nil {
		t.Fatal(err)
	}

	shell, proc := "", ""

	for line := range strings.SplitSeq(strings.TrimSpace(string(got)), "\n") {
		if v, ok := strings.CutPrefix(line, "shell="); ok {
			shell = strings.TrimSpace(v)
		}

		if v, ok := strings.CutPrefix(line, "Pid:"); ok {
			proc = strings.TrimSpace(v)
		}
	}

	if shell == "" || proc == "" {
		t.Fatalf("the step said %q, which is not two accounts of a pid", string(got))
	}

	if shell != proc {
		t.Errorf("the step is pid %s and its /proc says %s"+
			"\n  a step in its own PID namespace needs a /proc mounted in that"+
			" namespace, or everything reading /proc/$$ reads another process",
			shell, proc)
	}
}
