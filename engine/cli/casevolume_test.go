package cli

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The remedy the note offers is run, and the volume it makes is case-sensitive.
//
// This exists because advice that does not work is worse than none: an earlier
// message here suggested building for another architecture, which moved the
// failure to `exec format error` and called it a fix. A recipe printed to a user
// who is already stuck has to be the recipe that works, and the only way to know
// that a year from now is to run it.
//
// Slow by the standards of this package - it creates and attaches a disk image -
// so it is skipped in short mode. It reaches no network.
func TestTheCaseSensitiveVolumeRecipeWorks(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("creates and attaches a disk image")
	}

	_, err := osexec.LookPath("hdiutil")
	if err != nil {
		t.Skip("hdiutil is not installed")
	}

	// Not t.TempDir for the mount point: a mounted volume is not a directory
	// the cleanup can remove, and detaching is what has to happen first.
	base := t.TempDir()
	img := filepath.Join(base, "case-probe")
	mount := filepath.Join(base, "mnt")

	t.Cleanup(func() {
		_, _ = run(t, "hdiutil", "detach", "-force", mount)
	})

	for _, line := range caseVolumeRecipe(img, mount, testCacheDirEnv) {
		// Only the commands: the recipe ends with an `export`, which is
		// something for the user's shell rather than something to run here.
		if strings.HasPrefix(line, "export ") {
			continue
		}

		out, err := runLine(t, line)
		if err != nil {
			t.Fatalf("%s\n%v\n%s", line, err, out)
		}
	}

	store := filepath.Join(mount, "store")

	err = os.MkdirAll(store, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	if !caseSensitiveStore(store) {
		t.Error("the volume the note tells people to make is case-insensitive")
	}
}

// runLine runs one line of the recipe the way a user would: through a shell, so
// the quoting in the printed command is the quoting under test.
func runLine(t *testing.T, line string) (string, error) {
	t.Helper()

	return run(t, "sh", "-c", line)
}

func run(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	out, err := osexec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec // a test's own argv

	return string(out), err
}
