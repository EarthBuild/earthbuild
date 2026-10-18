//go:build linux

package trace

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// The reason a tracer stopped has to leave the process at the moment it stops.
//
// It used to be recorded and then reported through the step's error, which a
// hung step never returns - so the one state the report exists to explain was
// the one state it could not reach. Three captures of a stalled build came back
// with an empty verdict for that reason and were read as evidence about which
// exit the loop took, which they were not (E522).
func TestStoppedIsReportedWhenItHappens(t *testing.T) {
	t.Parallel()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	defer func() { _ = r.Close(); _ = w.Close() }()

	// **One descriptor, one owner.** `NewTracer` takes a number, so this held
	// the pipe twice: `tr.Close()` raw-closed it and the deferred `r.Close()`
	// closed it again. Between the two, any parallel test in this package could
	// be handed that number - and `Tracer.Close` says what happens then in its
	// own words: "closing it twice can take away somebody else's file".
	//
	// Seen as an unrelated test failing in its cleanup, which is what a stolen
	// descriptor looks like from the far end:
	//
	//	TestAStaleMemoryFileIsReopenedRatherThanBelieved
	//	  TempDir RemoveAll cleanup: readdirent 001: bad file descriptor
	//
	// `fromFile` hands the file over instead, and the tracer then closes
	// through it - so the second close is the no-op `*os.File` guarantees
	// rather than a raw close of a number somebody else now holds.
	tr := fromFile(r)
	defer func() { _ = tr.Close() }()

	var out strings.Builder

	tr.Report = &out

	tr.stopped(errors.New("the listener reported POLLHUP"))

	if got := out.String(); !strings.Contains(got, "POLLHUP") {
		t.Fatalf("a stop was not reported as it happened, only recorded: %q", got)
	}

	// First one wins, matching Stopped: a loop that stops usually stops for one
	// reason and reports it once, and a repeated line reads as a second fault.
	tr.stopped(errors.New("a later and less interesting reason"))

	if strings.Contains(out.String(), "less interesting") {
		t.Fatalf("a later stop overwrote the first report:\n%s", out.String())
	}
}
