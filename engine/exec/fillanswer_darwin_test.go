//go:build darwin

package exec

import (
	"errors"
	"strings"
	"testing"
)

// TestTheFillerIsReadWhenTheQuestionArrives.
//
// **The relay outlived the capture.** A sandbox is found and reused by name and
// outlives any one build, and the filler is set per build - so a relay that
// took the filler when it started would answer this build's questions with the
// last build's, or with nothing at all. `progressAnswer` has read its answerer
// late since it was written for exactly this reason; the filler was captured
// once instead, which was safe only while the relay refused to start without
// one.
//
// Streaming a blob became a second reason to start it, and on macOS the default
// one. The relay then began at boot with no filler, held that nil for the life
// of the sandbox, and the first step that needed a path faulted in got a
// segfault - `could not obtain /usr/local/sbin/cat` was the build that found it
// (E811).
func TestTheFillerIsReadWhenTheQuestionArrives(t *testing.T) {
	a := NewApple()

	err := a.fillAnswer("h", "/usr/local/sbin/cat")
	if err == nil {
		t.Fatal("a sandbox with no filler accepted a fault-in request")
	}

	if !strings.Contains(err.Error(), "/usr/local/sbin/cat") {
		t.Errorf("the refusal does not name the path:\n  %v", err)
	}

	// Set after the relay would have started, which is the whole point.
	asked := ""
	sentinel := errors.New("the filler ran")

	a.SetFill(func(_, path string) error { asked = path; return sentinel })

	err = a.fillAnswer("h", "/bin/sh")
	if !errors.Is(err, sentinel) {
		t.Errorf("a filler set after the relay started was not used: %v", err)
	}

	if asked != "/bin/sh" {
		t.Errorf("the filler was asked about %q, want /bin/sh", asked)
	}
}
