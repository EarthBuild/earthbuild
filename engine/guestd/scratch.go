package guestd

import (
	"fmt"
	"os"
)

// mountScratch makes a directory for `mount` to use, and keeps it only if the
// mount worked.
//
// A directory made for a mount is *owned by* the mount: if the mount fails there
// is nothing to hold it open, nothing will ever look in it, and it is litter. It
// was not treated that way, and the guest left one behind on every start that
// could not mount a procfs - 1625 of them on the build box (E473).
//
// The mount is a parameter so the ownership rule can be tested without one:
// mounting needs a namespace and a privilege, and *the rule under test is about
// the directory rather than about the mount*.
func mountScratch(prefix string, mount func(dir string) error) (string, error) {
	// Somewhere writable, chosen by the same rules as everything else the guest
	// scratches: `/run` is read-only in the sandbox image, which is the first
	// place this was tried.
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", fmt.Errorf("nowhere to scratch: %w", err)
	}

	err = mount(dir)
	if err != nil {
		// The removal's own failure is not reported: the mount's failure is the
		// news, and a second error about the cleanup would bury it.
		_ = os.RemoveAll(dir)

		return "", err
	}

	return dir, nil
}
