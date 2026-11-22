package overlay

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestAMissingElementSaysHowMuchRoomTheStoreHas.
//
// **The cause and the symptom were ten lines apart and never joined.** A worker
// on a disk something else had filled emptied its store to chase a free-space
// target it could not reach, and the next thing the *driver* saw - on another
// machine, in another log - was a delegated step refused because a layer was
// not there. Nothing in that refusal said the store had been emptied, or why,
// so the fleet read as broken and the disk read as fine (E-F1).
//
// Free space is the next question anybody asks on seeing this error, so it is
// answered in the error rather than thresholded: a rule about when it is
// "low enough to mention" is a rule that will be wrong on somebody's machine.
func TestAMissingElementSaysHowMuchRoomTheStoreHas(t *testing.T) {
	t.Parallel()

	err := missingElement(ir.NodeID{}, "/store/layers/abc", "/store/abc.decl", 240<<20, nil)

	said := err.Error()

	for _, want := range []string{
		"holds neither a layer nor a declaration",
		"/store/layers/abc",
		"240 MiB free",
	} {
		if !strings.Contains(said, want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, said)
		}
	}
}

// TestAMissingElementWithNoFreeReadingStillExplainsItself. A statfs that failed
// must not cost the reader the rest of the message.
func TestAMissingElementWithNoFreeReadingStillExplainsItself(t *testing.T) {
	t.Parallel()

	err := missingElement(ir.NodeID{}, "/store/layers/abc", "/store/abc.decl", 0,
		noReadingError{})

	said := err.Error()
	if !strings.Contains(said, "holds neither a layer nor a declaration") {
		t.Errorf("the refusal lost its explanation:\n%s", said)
	}

	if strings.Contains(said, "free") {
		t.Errorf("a reading that failed was reported as a figure:\n%s", said)
	}
}

type noReadingError struct{}

func (noReadingError) Error() string { return "no reading" }
