package exec

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// claimStore takes exclusive use of a store device for as long as this process
// holds the returned release.
//
// **Because two sandboxes mounting one device would destroy it.** A block
// device is not a directory: the store directory is shared by design and has a
// concurrency story, and two guests mounting one XFS filesystem read-write is
// not a race that loses an update - it is two kernels with two independent logs
// writing the same metadata. Nothing about a build makes that recoverable.
//
// `flock`, so the claim dies with the process. A lock file holding a pid would
// outlive a build killed with SIGKILL and leave the device unusable until
// somebody deleted it by hand, which is the failure mode of every lock file
// ever written. The kernel drops this one when the descriptor closes, however
// the process ended.
//
// Advisory, and that is enough: the only thing that attaches this device is
// this engine, and a VMM started by hand is a person who has decided to.
func claimStore(at string) (release func(), err error) {
	if at == "" {
		return func() {}, nil
	}

	f, err := os.OpenFile(at, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open the store device %s: %w", at, err)
	}

	// Non-blocking: a second build waiting would look like a hang, and the
	// honest answer is that this machine's store is busy. Blocking would also
	// be wrong for the common case, which is not two builds racing but one
	// build started while another is still running.
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err != nil {
		_ = f.Close()

		return nil, fmt.Errorf("the store device %s is in use by another build: %w"+
			"\n  a device holds one filesystem and two guests mounting it would"+
			" destroy it, so this build is refused rather than queued"+
			"\n  point EARTH_VM_STORE at a device of its own to build alongside",
			at, err)
	}

	return func() { _ = f.Close() }, nil
}
