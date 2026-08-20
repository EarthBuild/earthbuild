package fleet

import (
	"context"
	"testing"
	"time"
)

// Waiting for workers means waiting for workers that can be given work.
//
// A worker becomes *connected* the moment its dial lands and *placeable* only
// when it has said what it runs: placement refuses a worker with no platform, so
// an inventory entry without one is a machine the scheduler will step over
// (E503). The barrier counted connections, so a driver could report `1 worker(s)
// joined`, place nothing on it, and build everything locally - which is a slow
// local build wearing a fleet's clothes.
//
// On one machine the two are the same instant and the bug is invisible. Over a
// relay the declaration arrives a round trip later, and every step ran on the
// driver (E505).
//
// *A barrier that counts connections is not a barrier on readiness.*
func TestWaitingForWorkersWaitsForOnesThatCanBeGivenWork(t *testing.T) {
	t.Parallel()

	r := &Rendezvous{}
	id := r.add(nil)

	// Connected, and it has not said what it is.
	early, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if got := r.WaitFor(early, 1); got != 0 {
		t.Errorf("counted %d worker(s) before any of them said what they run"+
			"\n  placement refuses a worker with no platform, so this is a fleet of nobody", got)
	}

	r.note(id, "", "linux/arm64", 4)

	later, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()

	if got := r.WaitFor(later, 1); got != 1 {
		t.Errorf("counted %d worker(s) after one declared linux/arm64, want 1", got)
	}
}

// A worker that connects and never declares does not hold the build up.
//
// It cannot be placed on, so waiting the full deadline for it buys nothing. The
// driver degrades to a local build, which is what it would have done anyway -
// only sooner, and saying so.
func TestAWorkerThatNeverSaysWhatItIsDoesNotBlockForever(t *testing.T) {
	t.Parallel()

	r := &Rendezvous{}
	r.add(nil)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()

	if got := r.WaitFor(ctx, 1); got != 0 {
		t.Errorf("counted %d, want 0", got)
	}

	if took := time.Since(start); took > time.Second {
		t.Errorf("waited %v for a worker that cannot be placed on", took)
	}
}
