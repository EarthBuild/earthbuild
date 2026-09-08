package exec

import (
	"fmt"
	"os"
	"time"

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

	// **Non-blocking, but not instant.** A second build waiting on a first
	// would look like a hang, and the honest answer is that this machine's
	// store is busy - so this never waits for a build. What it does wait for is
	// a build that has *ended*: the previous sandbox's VMM releases the device
	// as it goes away, and a gate that starts the next build immediately is
	// refused by a guest that is already on its way out.
	//
	// That is not a hypothesis. A serial corpus run - one build at a time, by
	// construction - was refused 36 times in 246, and each time /proc/locks
	// named no holder microseconds later: the lock was held when flock asked
	// and gone when the message was written.
	//
	// Short enough that a device held by a real build still says so while the
	// reader is watching, which is what stops this becoming the hang above.
	err = flockWithin(f, storeClaimPatience)
	if err != nil {
		_ = f.Close()

		return nil, fmt.Errorf("the store device %s is in use by another build: %w%s"+
			"\n  a device holds one filesystem and two guests mounting it would"+
			" destroy it, so this build is refused rather than queued"+
			"\n  point EARTH_VM_STORE at a device of its own to build alongside",
			at, err, whoHolds(at))
	}

	return func() { _ = f.Close() }, nil
}

// storeClaimPatience is how long a claim waits for a departing guest.
//
// Ten seconds: a teardown is well under one, and ten is enough headroom for a
// loaded machine without being long enough to read as a hang. A device held by
// a build that is genuinely running is refused after this, with the same
// message it always gave.
const storeClaimPatience = 10 * time.Second

// flockWithin takes an exclusive lock, retrying while it is held.
//
// Polled rather than blocking, because a blocking flock cannot be given a
// deadline: LOCK_EX without LOCK_NB waits for as long as the holder lives, and
// the whole point here is to wait for a holder that is leaving and not for one
// that is staying.
func flockWithin(f *os.File, within time.Duration) error {
	var (
		err   error
		start = time.Now()
	)

	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil || time.Since(start) >= within {
			return err //nolint:wrapcheck // the caller writes the diagnosis
		}

		time.Sleep(storeClaimPoll)
	}
}

// storeClaimPoll is how often the claim asks again. Small against the patience
// and large against the syscall, so a departing guest is noticed at once and a
// busy one is not asked ten thousand times.
const storeClaimPoll = 20 * time.Millisecond
