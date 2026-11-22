package overlay

import (
	"fmt"
	"os"
	"path/filepath"
)

// shortNameLen is how much of a layer's identity a mount needs to name it.
//
// 12 hex characters is 48 bits. Two layers in one stack colliding is not a
// correctness question here anyway - a collision is detected and the full path
// used instead - so this is chosen for the option budget rather than against
// birthday arithmetic.
const shortNameLen = 12

// link gives a layer a short name under dir, and returns the path to use.
//
// Every byte of a lowerdir path is charged against the option page, and a layer
// named by its full digest under the store costs 98 of them: 41 layers of that
// is 4140 bytes against a limit of 4095, which the kernel reports as ENOENT
// naming nothing (see lowerHint). A symlink farm is what the container runtimes
// do about it, and it costs one symlink per layer per store.
//
// The farm lives in *scratch*, never in the layer store: the store arrives over
// a shared mount and is read-only to the guest by design, so a store that
// happened to be writable is not something to rely on.
//
// Falls back to the target itself rather than failing. A mount that works with
// long paths must not be turned into a mount that does not work at all because
// an optimisation could not be applied - and the caller finds out either way,
// because the option string is measured before it is used.
func link(dir, target, id string) string {
	if len(id) < shortNameLen {
		return target
	}

	name := filepath.Join(dir, id[:shortNameLen])

	// Already there and pointing at the right layer is the common case: a
	// second build reusing a cached layer, or two steps standing on one base.
	at, err := os.Readlink(name)
	if err == nil {
		if at == target {
			return name
		}

		// A short name that means something else. Rare enough to be worth no
		// cleverness and dangerous enough to be worth no guessing.
		return target
	}

	// Private: a directory this engine invents for its own bookkeeping, not
	// one whose mode a build decided (gosec G301).
	err = os.MkdirAll(dir, 0o750)
	if err != nil {
		return target
	}

	err = os.Symlink(target, name)
	if err == nil {
		return name
	}

	// Lost a race with another mount, or something else is in the way. Read it
	// back rather than trusting the error: a concurrent creator with the same
	// target has done exactly the work this call wanted done.
	at, err = os.Readlink(name)
	if err == nil && at == target {
		return name
	}

	return target
}

// tooLong reports an option string the kernel will not read whole.
//
// Checked before the mount rather than diagnosed after it, so the message names
// the cause instead of describing the symptom the truncation produced.
func tooLong(opts string, n int, shortened bool) error {
	if len(opts) <= maxMountOptions {
		return nil
	}

	// The cause, when the cause is a missing facility rather than the build.
	//
	// Lower layers are named through /proc/self/fd, which is eighteen bytes and
	// does not vary with the store. Where that is unavailable the code falls
	// back to the paths it was given - correct, and much longer - and a stack
	// that fits comfortably elsewhere stops fitting here.
	//
	// I11: a degradation is always reported with its cause. Without this line
	// the refusal blames the stack and sends the reader to restructure a build
	// that is not the problem.
	why := ""
	if !shortened {
		why = "\n  the short form (/proc/self/fd) was not available here, so the" +
			"\n  paths are the store's own and this stack may fit on a machine that has it"
	}

	return fmt.Errorf(
		"a stack of %d layers needs %d bytes of mount options and the kernel reads %d"+
			"\n  this is a limit on the length of the paths, not on how many layers overlayfs"+
			"\n  will stack - it is reached long before MaxStackDepth"+
			"\n  the build has to flatten (green paper 4.8) before it can be mounted%s",
		n, len(opts), maxMountOptions, why)
}
