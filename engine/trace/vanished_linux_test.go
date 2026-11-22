//go:build linux

package trace

import (
	"errors"
	"testing"
)

// TestACallThatNeverHappenedIsNotAMissedObservation.
//
// **The rule both halves of the race collapse to.** A notification carries a
// pid and an address; reading the path means reading that process's memory, and
// between the two the process may be gone. `SECCOMP_IOCTL_NOTIF_ID_VALID` is
// what asks - and it was written, documented as required by two files, and
// called by nothing but the tests.
//
// The consequence of not asking is different on each side:
//
//   - the read *failed*, because the address is no longer mapped. The step is
//     declared incomplete and can never earn an L2 hit again (I3) - for a
//     syscall that did not happen.
//   - the read *worked*, on a pid the kernel has since handed to somebody else.
//     Then the engine records an unrelated program's memory as a path the step
//     opened, which is the far worse half and is what the comment on
//     `stillRunning` was written about.
//
// A notification that is no longer valid means the call never completed, so
// there is nothing to record and nothing to declare missing.
func TestACallThatNeverHappenedIsNotAMissedObservation(t *testing.T) {
	t.Parallel()

	unreadable := errors.New("input/output error")

	for _, c := range []struct {
		err   error
		valid bool
		want  sighting
		what  string
	}{
		{nil, true, sightingRecord, "read it, and the caller is still stopped in the call"},
		{nil, false, sightingDrop, "read it, but the pid may since be somebody else's"},
		{unreadable, true, sightingLose, "genuinely could not read a path the step named"},
		{unreadable, false, sightingDrop, "could not read it because the call never happened"},
		{errNoPathNamed, true, sightingDrop, "there was never a path: glibc probing for statx"},
		{errNoPathNamed, false, sightingDrop, "no path, and gone as well"},
	} {
		if got := whatToDoWith(c.err, c.valid); got != c.want {
			t.Errorf("err=%v valid=%v gave %v, wanted %v (%s)",
				c.err, c.valid, got, c.want, c.what)
		}
	}
}

// TestLosingIsTheOnlyOutcomeThatCostsTheTier states which of the three is the
// expensive one, so that a future change making everything `lose` for safety is
// caught by a test rather than by a build that quietly stopped caching.
func TestLosingIsTheOnlyOutcomeThatCostsTheTier(t *testing.T) {
	t.Parallel()

	if whatToDoWith(errors.New("io"), false) == sightingLose {
		t.Error("a call that never happened marks the observation incomplete," +
			" so a step that spawns short-lived processes can never earn an L2 hit")
	}

	if whatToDoWith(errNoPathNamed, true) == sightingLose {
		t.Error("a call naming no path marks the observation incomplete, so any" +
			" step whose processes probe for statx can never earn an L2 hit")
	}
}
