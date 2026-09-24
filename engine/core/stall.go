package core

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// DefaultStall is how long a build may make no progress before saying so.
//
// Long enough that a slow but healthy step - a base image over a thin link, a
// compile of a large tree - is never called stuck, and short enough to be seen
// before a CI timeout kills the run and takes the evidence with it.
const DefaultStall = 5 * time.Minute

// inflight is a step that has started and not yet finished.
type inflight struct {
	where string // the Earthfile line, for a reader to go and look at
	what  string // the command, which is what they will recognise
	since time.Time
}

// stalled tracks steps in flight so a build that stops moving can say what it
// is waiting on.
//
// **The one failure the rest of the reporting cannot describe.** Every outcome
// this engine records is written when a step *finishes*: the record, the cache
// line, the step error. A step that never finishes writes none of them, so a
// hung build is not merely badly reported - it is reported identically to a
// build that is working, right up until something outside kills it.
//
// Observed in the microVM backend. A step held an outbound connection that the
// remote accepted and never answered; with no timeout anywhere in the chain the
// step waited, the scheduler waited on the step, and an `ARG` substitution
// waited on the scheduler. The build printed nothing for 900 seconds and was
// killed by `timeout`, whose exit code was then mistaken for the engine's. The
// diagnosis needed a goroutine dump, which is not a thing a user can be asked
// for.
//
// Progress is *any* step starting or finishing, not a particular one. A build
// that is completing steps is working however long its slowest step has been
// running, and reporting on age alone would cry wolf at every large compile.
type stalled struct {
	mu    sync.Mutex
	at    map[ir.NodeID]inflight
	moved time.Time
	// next is how long the stall must last before it is worth saying again,
	// doubling each time it is said. **A warning that repeats every tick is a
	// warning people turn off**, and the condition here is one a legitimately
	// long step satisfies while doing exactly what it should. Reset by any
	// progress, so a second stall is not made to serve the first one's
	// patience.
	next time.Duration
}

// newStalled starts tracking, with now as the last time the build moved.
func newStalled(now time.Time) *stalled {
	return &stalled{at: map[ir.NodeID]inflight{}, moved: now}
}

// progress records that the build moved, which resets both clocks.
//
// Called with the lock held.
func (s *stalled) progress(now time.Time) {
	s.moved = now
	s.next = 0
}

// begin records that a step has started.
func (s *stalled) begin(id ir.NodeID, where, what string, now time.Time) {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.at[id] = inflight{where: where, what: what, since: now}
	s.progress(now)
}

// end records that a step has finished, however it finished.
func (s *stalled) end(id ir.NodeID, now time.Time) {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.at, id)
	s.progress(now)
}

// note describes the stall, or empty if the build is not stalled.
//
// Ordered oldest-first, ties broken on identity, because this text is what a
// reader pastes into a bug report: two runs of one hang have to produce the
// same words, or a reshuffled list reads as a changed symptom.
func (s *stalled) note(now time.Time, after time.Duration) string {
	if s == nil {
		return ""
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.next == 0 {
		s.next = after
	}

	if len(s.at) == 0 || now.Sub(s.moved) < s.next {
		return ""
	}

	// Doubled before the note is built, so the next one is due twice as far
	// out however this one is rendered.
	s.next *= 2

	ids := make([]ir.NodeID, 0, len(s.at))
	for id := range s.at {
		ids = append(ids, id)
	}

	sort.Slice(ids, func(i, j int) bool {
		a, b := s.at[ids[i]], s.at[ids[j]]
		if !a.since.Equal(b.since) {
			return a.since.Before(b.since)
		}

		return ids[i].String() < ids[j].String()
	})

	var b strings.Builder

	fmt.Fprintf(&b, "warning: nothing has started or finished for %s\n",
		now.Sub(s.moved).Round(time.Second))

	for _, id := range ids {
		f := s.at[id]
		fmt.Fprintf(&b, "  %-14s %-8s %s\n",
			f.where, now.Sub(f.since).Round(time.Second), f.what)
	}

	// **Facts, not a prophecy.** An earlier draft ended "and will not stop on
	// its own" and named the network as the usual cause - and the first real
	// firing was a `RUN sleep 400`, which was neither stuck nor networked. A
	// diagnostic that guesses is one a reader learns to discount, so what is
	// merely true goes here and what depends on evidence is added by whoever
	// holds the evidence. See cli.netLine, which speaks only when it has
	// counted the bytes.
	b.WriteString("  no step has a timeout, so one that is stuck stays stuck\n")

	return b.String()
}
