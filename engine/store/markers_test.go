package store

import (
	"os"
	"regexp"
	"testing"
)

// The reader's markers are the writer's markers.
//
// `engine/guest/whiteout.go` writes `.wh.<name>` into a committed layer and
// `engine/exec/view.go` reads it. They are two parties to one wire format,
// across a process boundary, and the reader cannot import the writer's
// unexported constants - so it declares its own, and nothing made them agree.
//
// A guard rather than a shared constant because the sharing is the problem: the
// guest is a separate binary that may be older than the host driving it, so the
// convention has to be *stated* on both sides. What must not happen is the two
// statements drifting silently, and a drifted reader reports every deleted file
// as present - a view that answers "still there" about something the step
// deleted, which is I3 with extra steps.
//
// Source-level, and worth what source guards are worth: it proves the literals
// match, never that a build reaches them. Its behavioural pair is
// `TestAViewAnswersFromTheStackWithoutMounting/a_deletion_in_a_higher_layer_hides_the_file`.
func TestTheWhiteoutMarkersMatchTheGuests(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../guest/whiteout.go")
	if err != nil {
		t.Fatalf("the writer's source is not where this expects it: %v", err)
	}

	want := map[string]string{"whPrefix": whPrefix, "whOpaque": whOpaque}

	for name, mine := range want {
		m := regexp.MustCompile(name + `\s*=\s*"([^"]*)"`).FindSubmatch(b)
		if m == nil {
			t.Errorf("the guest no longer declares %s, so this reader is guessing", name)

			continue
		}

		if string(m[1]) != mine {
			t.Errorf("the guest writes %s = %q and this reads %q:"+
				"\n  a view that does not recognise a deletion marker reports every"+
				"\n  deleted file as still present", name, m[1], mine)
		}
	}
}
