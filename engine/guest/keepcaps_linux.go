package guest

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// keepCapsWanted reports whether this step's capabilities are to survive the
// change of user. See EnvStepKeepCaps, and keepCapsEnv for who decides.
func keepCapsWanted() bool {
	return os.Getenv(EnvStepKeepCaps) == "1"
}

// holdCapsAcrossSetuid asks the kernel to leave the permitted set alone when the
// uid changes.
//
// `setuid` away from root clears every capability set, which is the kernel doing
// exactly what it should for an ordinary process. PR_SET_KEEPCAPS suspends that
// for the permitted set only - effective and inheritable are still cleared - so
// this is half the job and restoreCaps is the other half.
//
// Before the setgid too, not only the setuid: the flag survives both and losing
// the group first would leave nothing to restore.
func holdCapsAcrossSetuid() error {
	err := unix.Prctl(unix.PR_SET_KEEPCAPS, 1, 0, 0, 0)
	if err != nil {
		return fmt.Errorf("keep this step's capabilities across its change of user: %w", err)
	}

	return nil
}

// restoreCaps puts back what the setuid took, for a privileged step.
//
// Three sets and all three matter. **Permitted** survived because of
// PR_SET_KEEPCAPS. **Effective** is what the kernel actually checks, and is
// empty until it is written. **Ambient** is what an `execve` leaves in place:
// without it the step's own command starts with nothing, since a file with no
// capability bits grants none to a non-root uid however privileged its parent
// was - and every step ends in an exec, so the first two alone would be
// invisible.
//
// Inheritable is set because ambient may only hold what is in both permitted and
// inheritable; it is a precondition of the raise rather than a grant of its own.
//
// Reported rather than ignored on failure. A step that asked for privilege and
// quietly did not get it fails later, somewhere else, at whatever first needed
// it - which is the shape of every diagnosis this file exists to avoid (I11).
func restoreCaps() error {
	hdr := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}

	var data [2]unix.CapUserData

	err := unix.Capget(&hdr, &data[0])
	if err != nil {
		return fmt.Errorf("read this step's capabilities: %w", err)
	}

	for i := range data {
		data[i].Effective = data[i].Permitted
		data[i].Inheritable = data[i].Permitted
	}

	err = unix.Capset(&hdr, &data[0])
	if err != nil {
		return fmt.Errorf("restore this step's capabilities: %w", err)
	}

	for cap := 0; cap <= unix.CAP_LAST_CAP; cap++ {
		word, bit := cap/32, uint(cap%32)
		if data[word].Permitted&(1<<bit) == 0 {
			continue
		}

		err = unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_RAISE, uintptr(cap), 0, 0)
		if err != nil {
			return fmt.Errorf("carry capability %d into this step's command: %w", cap, err)
		}
	}

	return nil
}
