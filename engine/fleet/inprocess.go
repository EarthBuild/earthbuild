package fleet

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// InProcess is a fleet with no network, for tests and for a single machine.
//
// It is a real implementation rather than a mock: the control loop it exercises
// is the one a networked transport has to reproduce, so a property proved here -
// a cancel that stops a step, a disappearance that re-queues it - is a property
// of the protocol rather than of a particular transport.
//
// A single-machine fleet is also a useful thing to have: it is what `--workers`
// means before anybody has a second machine, and it makes the delegation path
// exercised by every build rather than only by a fleet nobody runs locally.
type InProcess struct {
	mu      sync.Mutex
	workers []*worker
	next    int
}

// worker is one participant's handler.
type worker struct {
	run   func(context.Context, Assignment) (Reply, error)
	alive bool
}

// AddWorker admits a participant that runs assignments with this function.
func (f *InProcess) AddWorker(run func(context.Context, Assignment) (Reply, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.workers = append(f.workers, &worker{run: run, alive: true})
}

// Kill makes a worker stop answering, as one that lost its network would.
func (f *InProcess) Kill(i int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if i >= 0 && i < len(f.workers) {
		f.workers[i].alive = false
	}
}

// Workers is how many are reachable.
func (f *InProcess) Workers() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	n := 0

	for _, w := range f.workers {
		if w.alive {
			n++
		}
	}

	return n
}

// Assign gives a step to a worker, and re-queues it if that worker disappears.
//
// The re-queue is C.5 and is sound because steps are pure (I1): a step that
// vanished with its worker can be run again anywhere, and the second attempt
// produces the same result as the first would have. It is the same property that
// makes retry safe (I7).
//
// Bounded by the number of workers rather than by a retry count: each is tried
// at most once, so a fleet where every worker has died fails immediately instead
// of retrying its way through a long timeout.
func (f *InProcess) Assign(ctx context.Context, a Assignment) (Reply, error) {
	// A snapshot, and each entry used at most once.
	//
	// The first version asked for "the next live worker" in a loop, which hands
	// the same one back for ever when it keeps disappearing - `Assign` does not
	// mark a worker dead, deliberately, because a transport that decided a peer
	// was gone from one failed step would evict the fleet on a network blip.
	// The bound therefore has to live in this call, and the comment saying so
	// was written before the code did it (E234).
	order := f.snapshot()
	if len(order) == 0 {
		return Reply{}, ErrNoWorker
	}

	tried := 0

	for _, w := range order {
		tried++

		r, err := w.run(ctx, a)

		switch {
		case err == nil:
			return r, nil

		case errors.Is(err, ErrWorkerGone):
			// Round again, on somebody else. Deliberately not counted as a
			// failure of the step: nothing about the step went wrong, and a
			// pure step can be run anywhere (I1, I7, C.5).
			continue

		default:
			// The worker answered and the answer was an error. That is a
			// result, not a disappearance, and re-queueing it would run a
			// failing step on every machine in turn.
			return Reply{}, err
		}
	}

	return Reply{}, fmt.Errorf("%w after %d attempt(s)", ErrWorkerGone, tried)
}

// snapshot is the live workers, starting from wherever the last call left off.
//
// Round-robin so that one worker is not given every step, and a snapshot so that
// a worker added mid-assignment does not extend a loop that is trying to end.
func (f *InProcess) snapshot() []*worker {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]*worker, 0, len(f.workers))

	for i := range f.workers {
		w := f.workers[(f.next+i)%len(f.workers)]
		if w.alive {
			out = append(out, w)
		}
	}

	if len(f.workers) > 0 {
		f.next++
	}

	return out
}
