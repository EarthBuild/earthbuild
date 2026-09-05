package overlay

import (
	"errors"
	"os"
)

// Mountable returns a directory under which stacks can be mounted, trying
// harder than one location before giving up.
//
// The first choice is what the caller asked for. When that is refused because
// the machine cannot mount there - a working directory already on overlayfs,
// which is every container's root and where this repository's own `+unit-test`
// runs - a tmpfs is tried instead, because overlayfs will stack on one.
//
// This is the engine's own diagnostic taken at its word. `mountHint` has told
// anyone who hit this to "put the engine's working directory on a real
// filesystem: mount a volume or a tmpfs" since long before anything did it, and
// a suite that skipped rather than following its own advice left the Linux
// materialiser untested in CI for the entire life of the branch (E69).
//
// The cleanup is always non-nil on success and must be called: the last resort
// *mounts* a tmpfs, and a caller that forgets leaves one behind.
//
// Returns the empty string and the original error when nowhere works, so a
// caller can still tell "not here" from "broken": an error that is not
// ErrUnavailable is the materialiser failing and must not become a skip.
func Mountable(preferred string) (dir string, cleanup func(), err error) {
	err = Available(preferred)
	if err == nil {
		return preferred, func() {}, nil
	}

	if !errors.Is(err, ErrUnavailable) {
		return "", nil, err
	}

	// Somewhere that is not the container's overlay root. A tmpfs already there
	// is preferred; the build image this runs in has none, so one is made.
	for _, base := range []string{"/dev/shm", os.Getenv("EARTH_TEST_TMPFS")} {
		if base == "" {
			continue
		}

		// base comes from this file's own candidate list and the name from
		// MkdirTemp, so nothing a build says reaches either.
		alt, mkErr := os.MkdirTemp(base, "earth-overlay-*")
		if mkErr != nil {
			continue
		}

		if Available(alt) == nil {
			return alt, func() { _ = os.RemoveAll(alt) }, nil
		}

		_ = os.RemoveAll(alt)
	}

	// Made, and handed back with the way to remove it. A finalizer was the
	// first version of this and would have been a mount that survives the
	// process that made it whenever the garbage collector did not get round to
	// it - which is most of the time.
	made, undo, mkErr := tmpfs()
	if mkErr == nil {
		if Available(made) == nil {
			return made, undo, nil
		}

		undo()
	}

	return "", nil, err
}
