package guest

import (
	"os"
	"path/filepath"
	"testing"
)

// A step can write to /dev/stdout.
//
// `/dev` is a tmpfs this engine makes, and a tmpfs starts empty: the four names
// a shell expects to find there are symlinks into /proc/self/fd, and nothing
// created them. Missing, `< /dev/stdin` and `ls /dev/fd` fail with a message
// naming the path, which is at least legible. `> /dev/stdout` does not fail -
// the shell creates a regular file called `stdout` in the tmpfs, writes to it,
// and the tmpfs is discarded with the step. The output is gone and nothing
// said so (E756).
func TestAStepCanWriteToDevStdout(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.MkdirAll(filepath.Join(root, "dev"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = linkStdio(root)
	if err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{
		"dev/fd":     "/proc/self/fd",
		"dev/stdin":  "/proc/self/fd/0",
		"dev/stdout": "/proc/self/fd/1",
		"dev/stderr": "/proc/self/fd/2",
	} {
		got, readErr := os.Readlink(filepath.Join(root, name))
		if readErr != nil {
			t.Errorf("%s is not a symlink: %v", name, readErr)

			continue
		}

		if got != want {
			t.Errorf("%s points at %s, want %s", name, got, want)
		}
	}
}

// An image that ships its own is left alone.
//
// Some images make these themselves, and one that did would otherwise stop a
// step from starting for a link that is already correct. Reported only if it
// cannot be created *and* is not there.
func TestStdioLinksAnImageAlreadyHasAreLeftAlone(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dev := filepath.Join(root, "dev")

	err := os.MkdirAll(dev, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	// Pointing somewhere else entirely, so it is distinguishable from one this
	// engine would have made.
	err = os.Symlink("/proc/self/fd/9", filepath.Join(dev, "stdout"))
	if err != nil {
		t.Fatal(err)
	}

	err = linkStdio(root)
	if err != nil {
		t.Fatalf("an image's own /dev/stdout was an error: %v", err)
	}

	got, err := os.Readlink(filepath.Join(dev, "stdout"))
	if err != nil {
		t.Fatal(err)
	}

	if got != "/proc/self/fd/9" {
		t.Errorf("the image's /dev/stdout was replaced with %s", got)
	}
}
