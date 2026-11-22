package guest

import (
	"testing"

	"golang.org/x/sys/unix"
)

// A step's umask is this engine's, not its caller's.
//
// Not parallel, and restored: a umask belongs to the process, so a test that
// changed one and left would change what every other test's files are created
// with.
//
// The mask a step inherits decides the mode of every file it creates, and those
// modes are in the layer's digest. Inherited, the same build under `umask 077`
// produced `-rw-------` where it had produced `-rw-r--r--` - a different layer,
// under a key that says nothing about umask, so a fleet worker or a differently
// configured shell silently produced a layer that does not match (E759).
func TestAStepsUmaskIsFixed(t *testing.T) {
	was := unix.Umask(0o077)
	defer unix.Umask(was)

	setStepUmask()

	got := unix.Umask(0o077)
	unix.Umask(got)

	if got != stepUmask {
		t.Errorf("a step would create files under umask %#o, want %#o", got, stepUmask)
	}
}

// The mask is the conventional one.
//
// 022 is what a container runtime gives a step and what every image is built
// expecting: files readable by whoever runs the image, writable by their owner.
// A tighter one produces images whose files the image's own user cannot read,
// and the failure appears when the image is run rather than when it is built.
func TestTheStepUmaskIsTheConventionalOne(t *testing.T) {
	t.Parallel()

	if stepUmask != 0o022 {
		t.Errorf("stepUmask = %#o, want %#o", stepUmask, 0o022)
	}
}
