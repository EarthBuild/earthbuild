package guest

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A mount point that is already there is not opened again.
//
// Preparing a bind mount needs the target to *exist*; creating it by opening it
// is a means, not a requirement. Opening it unconditionally means opening
// whatever is already at that path - and by the time a second step prepares the
// same mount point, what is there is the device the first one bound.
//
// `/dev/tty` is the case that found this. Three concurrent steps share a root;
// the first binds /dev/tty over the target, the second opens it, and on a
// machine with no controlling terminal - which is every container, every CI
// runner and every daemon - that open returns ENXIO:
//
//	prepare the mount point /dev/tty: open /tmp/.../dev/tty:
//	no such device or address
//
// On a developer's machine there is a terminal, the open succeeds, and nothing
// is ever wrong (E52).
//
// A FIFO stands in for the device, because opening one for reading blocks until
// a writer arrives: an implementation that opens what is already there does not
// fail this test, it hangs, and the deadline turns that into a failure with a
// sentence on it.
func TestAnExistingMountPointIsNotOpened(t *testing.T) {
	t.Parallel()

	target := filepath.Join(t.TempDir(), "tty")

	err := syscall.Mkfifo(target, 0o600)
	if err != nil {
		t.Skipf("cannot make a fifo here: %v", err)
	}

	done := make(chan error, 1)

	go func() { done <- ensureFile(target, 0o666) }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("an existing mount point was rejected: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("preparing an existing mount point opened it and blocked")
	}
}

// A mount point that is not there is created.
//
// The other half: the target has to exist for a bind to land on it, so absence
// is the case that must still do the work.
func TestAMissingMountPointIsCreated(t *testing.T) {
	t.Parallel()

	target := filepath.Join(t.TempDir(), "nested", "file")

	err := ensureFile(target, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(target)
	if err != nil {
		t.Fatalf("the mount point was not created: %v", err)
	}

	if fi.IsDir() {
		t.Error("the mount point is a directory, and a file source cannot bind onto one")
	}
}
