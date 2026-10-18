//go:build linux

package trace

import "errors"

// errNoPathNamed says a call carried no path to read, as opposed to one this
// engine failed to read.
//
// **glibc probes for `statx` by calling it with nothing.** At startup it issues
// `statx(0, NULL, AT_STATX_SYNC_AS_STAT, STATX_ALL, NULL)` and looks at the
// answer: EFAULT means the kernel has the call, ENOSYS means it does not. The
// result is discarded and no file is involved - the kernel refuses it before
// looking at anything.
//
// Read as a path argument, that null pointer is address zero, nothing is mapped
// there, and `/proc/<pid>/mem` answers EIO. Which is indistinguishable from a
// path this engine could not read, unless somebody looks at the pointer.
//
// It happens once per process, and a Rust build makes processes by the hundred:
// `cargo install --root $CARGO_HOME` lost its observation on every build for a
// call that read nothing.
var errNoPathNamed = errors.New("this call names no path")

// namesNoPath reports that a path argument is a null pointer.
//
// The whole test, and deliberately no more than it. An address this engine
// cannot read is a real gap; an address that is *not an address* is not.
func namesNoPath(addr uint64) bool { return addr == 0 }

// sighting is what to do with one notification's path.
type sighting int

const (
	// sightingRecord keeps the path: it was read, and the caller is still
	// stopped in the call it named.
	sightingRecord sighting = iota
	// sightingDrop keeps nothing and loses nothing: the notification is no
	// longer outstanding, so the call did not complete and observed nothing.
	sightingDrop
	// sightingLose declares the observation incomplete, which costs the step
	// its observed-input tier for as long as the entry lives (I3).
	sightingLose
)

func (s sighting) String() string {
	switch s {
	case sightingRecord:
		return "record"
	case sightingDrop:
		return "drop"
	case sightingLose:
		return "lose"
	default:
		return "unknown"
	}
}

// whatToDoWith decides what one notification's path is worth, given how the
// read went and whether the notification is still outstanding.
//
// **Validity is asked after the read, and it answers two different questions.**
// A notification carries a pid and an address, and reading the path means
// reading that process's memory; between the two, the process may exit and the
// pid be handed to somebody else. `SECCOMP_IOCTL_NOTIF_ID_VALID` is the only
// thing that can tell, and checking it *before* the read proves the target was
// alive a moment ago, which is the wrong side of the race.
//
// Four cases, three outcomes:
//
//   - no longer valid: the call did not complete, so it opened nothing, and
//     anything read for it may be a pid's new owner. Record nothing, lose
//     nothing.
//   - read: the path is this step's.
//   - named no path: a null pointer, which is glibc probing for `statx`. There
//     was never a path here. See errNoPathNamed.
//   - unread: the step named something this engine could not read, and the
//     observation is incomplete (I3).
//
// The middle two are why this exists. Both were `lose`, and losing costs the
// step its observed-input tier for as long as the entry lives - so a build that
// spawns short-lived processes could never earn an L2 hit.
func whatToDoWith(err error, valid bool) sighting {
	if !valid {
		return sightingDrop
	}

	switch {
	case err == nil:
		return sightingRecord
	case errors.Is(err, errNoPathNamed):
		return sightingDrop
	default:
		return sightingLose
	}
}
