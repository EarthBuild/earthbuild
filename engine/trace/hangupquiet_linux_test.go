//go:build linux

package trace

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// A hang-up is not reported when the step is the only thing carrying the filter.
//
// **The same event means opposite things in the two arrangements.** Where the
// guest installs the filter on a thread of its own, that thread is still
// filtered when the listener hangs up, so the step's next intercepted syscall
// stops in the kernel with nothing coming to release it - which presented as a
// build hanging with no message anywhere, and is why this prints at all (E520,
// E521).
//
// Where the shim installs it, the step is the only carrier, so POLLHUP *is* the
// step exiting - the ordinary end of every traced step (E723, E729). Reported
// there, it puts a line reading "syscall tracer stopped" into the log for every
// step of every build: 45 of them in a 45-step build, none before the shim
// arrangement existed.
//
// Suppressed rather than downgraded, because it is not a lesser fault - it is
// not a fault. Anything else the loop stops for still prints, and `Stopped`
// still returns it either way, so a caller that cares is unaffected.
func TestAHangUpIsQuietWhenTheStepIsTheOnlyCarrier(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name  string
		sole  bool
		hung  bool
		quiet bool
	}{
		{"the shim's, hung up", true, true, true},
		{"the shim's, some other fault", true, false, false},
		{"the guest's own, hung up", false, true, false},
		{"the guest's own, some other fault", false, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			tr := &Tracer{Report: &out}
			tr.soleCarrier = c.sole
			tr.hungUp.Store(c.hung)

			tr.stopped(errors.New("the notification listener reported POLLHUP"))

			got := strings.Contains(out.String(), "syscall tracer stopped")
			if got == c.quiet {
				t.Errorf("printed=%v, want printed=%v\n  out: %q"+
					"\n  a hang-up is the ordinary end of a step the shim filtered,"+
					" and a fault for a thread the guest filtered - the same event,"+
					" and only the carrier tells them apart",
					got, !c.quiet, out.String())
			}
		})
	}

	// Whatever it printed, the error is still there to be asked for.
	var out bytes.Buffer

	tr := &Tracer{Report: &out}
	tr.soleCarrier = true
	tr.hungUp.Store(true)
	tr.stopped(errors.New("hung up"))

	if tr.Stopped() == nil {
		t.Error("a suppressed report also lost the error" +
			"\n  quiet is about the log, not about what the tracer knows:" +
			" a caller that asks must still be told")
	}
}
