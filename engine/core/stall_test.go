package core

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A build that stops making progress says what it is waiting on.
//
// A step that never returns is the one failure the rest of the reporting cannot
// describe: outcomes are recorded when a step *finishes*, so a step that does
// not finish leaves the record empty and the build silent. Observed in the
// microVM backend, where a step held an outbound connection the remote never
// answered and the build sat for 900 seconds and printed nothing - the whole
// diagnosis needed a goroutine dump, which is not available to a user.
func TestAStalledBuildNamesWhatItIsWaitingOn(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	s := newStalled(start)

	s.begin(nodeID(1), "Earthfile:9", "RUN curl https://example.invalid", start)

	// Not yet: a step is allowed to take longer than the threshold, so long as
	// the build as a whole is still moving.
	if note := s.note(start.Add(time.Minute), 5*time.Minute); note != "" {
		t.Fatalf("a build one minute in was called stalled: %s", note)
	}

	note := s.note(start.Add(6*time.Minute), 5*time.Minute)
	if note == "" {
		t.Fatal("a build with nothing happening for six minutes said nothing")
	}

	// Where, how long, and what - anything less and the reader still has to
	// guess which of a hundred parallel steps is the stuck one.
	for _, want := range []string{"Earthfile:9", "RUN curl https://example.invalid", "6m0s"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note does not say %q:\n%s", want, note)
		}
	}
}

// A step finishing is progress, even when others are still running.
func TestAFinishedStepResetsTheStallClock(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	s := newStalled(start)

	s.begin(nodeID(1), "Earthfile:1", "RUN sleep 600", start)
	s.begin(nodeID(2), "Earthfile:2", "RUN true", start)
	s.end(nodeID(2), start.Add(4*time.Minute))

	// Four minutes of the six have passed, but a step finished at minute four,
	// so the build was moving then and is not yet stalled.
	if note := s.note(start.Add(6*time.Minute), 5*time.Minute); note != "" {
		t.Fatalf("a build that completed a step two minutes ago was called stalled: %s", note)
	}

	note := s.note(start.Add(10*time.Minute), 5*time.Minute)
	if note == "" {
		t.Fatal("a build idle for six minutes after its last completion said nothing")
	}

	if strings.Contains(note, "RUN true") {
		t.Errorf("a step that already finished was reported as still running:\n%s", note)
	}
}

// Nothing running is not a stall - it is a build between steps, or over.
func TestAnIdleSchedulerIsNotAStall(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	s := newStalled(start)

	if note := s.note(start.Add(time.Hour), time.Minute); note != "" {
		t.Fatalf("a scheduler running nothing was called stalled: %s", note)
	}
}

// The note is ordered oldest-first and does not depend on map iteration.
//
// Determinism is not cosmetic here: this note is what a reader pastes into a
// bug, and two runs of one hang have to produce the same text or the reader
// cannot tell a changed symptom from a reshuffled list.
func TestTheStallNoteIsDeterministic(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)

	first := ""

	for i := range 20 {
		s := newStalled(start)
		// Inserted in a different order each time; the output must not follow.
		order := []int{1, 2, 3, 4}
		if i%2 == 1 {
			order = []int{4, 3, 2, 1}
		}

		for _, n := range order {
			s.begin(nodeID(byte(n)), "Earthfile:"+strconv.Itoa(n), "RUN step"+strconv.Itoa(n),
				start.Add(time.Duration(n)*time.Second))
		}

		note := s.note(start.Add(time.Hour), time.Minute)
		if note == "" {
			t.Fatal("four running steps produced no note")
		}

		// The listed steps, not the whole note: inserting in a different order
		// means the build last moved at a different time, and the header says
		// so truthfully. The ordering of the list is the property under test.
		listed := ""

		for _, line := range strings.Split(note, "\n") {
			if strings.HasPrefix(line, "  Earthfile:") {
				listed += line + "\n"
			}
		}

		if first == "" {
			first = listed
		} else if listed != first {
			t.Fatalf("two identical stalls listed steps differently:\n%s\n---\n%s", first, listed)
		}
	}
}

// nodeID makes a distinguishable id without depending on how one is derived.
func nodeID(b byte) ir.NodeID {
	var id ir.NodeID
	id[0] = b

	return id
}

// A stall that persists is mentioned less and less often.
//
// **A warning that repeats every tick is a warning people turn off.** The
// condition is "nothing has started or finished", which a legitimately long
// step - a large compile, a base image over a thin link - satisfies while doing
// exactly what it should. Saying so once is useful; saying so every seventy-five
// seconds for twenty minutes trains the reader to scroll past the one time it
// mattered.
//
// Doubling rather than once-only: a build that has been stuck for an hour
// should still say so, because the reader may have arrived after the first.
func TestARepeatedStallIsSaidLessOften(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	s := newStalled(start)
	s.begin(nodeID(1), "Earthfile:1", "RUN sleep 3600", start)

	const after = 5 * time.Minute

	// Ticking as the scheduler does, and recording when it actually spoke.
	var said []time.Duration

	for at := time.Second; at < 90*time.Minute; at += after / 4 {
		if s.note(start.Add(at), after) != "" {
			said = append(said, at)
		}
	}

	if len(said) < 3 {
		t.Fatalf("an hour and a half of stall was mentioned %d times, which is too few: %v", len(said), said)
	}

	if len(said) > 8 {
		t.Fatalf("an hour and a half of stall was mentioned %d times, which is spam: %v", len(said), said)
	}

	// Each gap at least as long as the one before it.
	for i := 2; i < len(said); i++ {
		prev, gap := said[i-1]-said[i-2], said[i]-said[i-1]
		if gap < prev {
			t.Errorf("the gap between mentions shrank: %v then %v (%v)", prev, gap, said)
		}
	}
}

// Progress resets the backoff, so the next stall is reported promptly.
//
// Without this a build that stalls, recovers and stalls again inherits the
// first stall's patience, and the second one - which is new information - waits
// out a delay earned by a problem that is over.
func TestProgressResetsTheBackoff(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	s := newStalled(start)
	s.begin(nodeID(1), "Earthfile:1", "RUN one", start)

	const after = 5 * time.Minute

	// Stall long enough to back off well past the threshold.
	for at := after; at < 2*time.Hour; at += after {
		s.note(start.Add(at), after)
	}

	// The build recovers, then stalls again.
	s.end(nodeID(1), start.Add(2*time.Hour))
	s.begin(nodeID(2), "Earthfile:2", "RUN two", start.Add(2*time.Hour))

	at := start.Add(2*time.Hour + after + time.Second)
	if note := s.note(at, after); note == "" {
		t.Fatal("a fresh stall after a recovery was not reported at the threshold")
	}
}
