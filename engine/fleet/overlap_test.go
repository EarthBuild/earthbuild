package fleet_test

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// slowFetch answers after a while, so a transfer can be seen to overlap.
type slowFetch struct {
	*fleet.LayerSource
	wait time.Duration

	mu   sync.Mutex
	when []window
}

// window is when something was happening, so two of them can be asked whether
// they were happening at once.
//
// Intervals rather than samples: the first replacement for this file's clock bar
// asked "is a fetch in flight" at the start and end of each step, and the fetch
// it was looking for began a hair after the first sample and ended a hair before
// the second. **A sample answers about an instant; the claim is about a
// stretch** (E481).
type window struct{ from, to time.Time }

// overlaps reports whether two stretches of time share any of it.
func (w window) overlaps(o window) bool {
	return w.from.Before(o.to) && o.from.Before(w.to)
}

func (s *slowFetch) Fetch(
	ctx context.Context, ids []ir.NodeID,
) (map[ir.NodeID]io.Reader, error) {
	began := time.Now()

	defer func() {
		s.mu.Lock()
		s.when = append(s.when, window{from: began, to: time.Now()})
		s.mu.Unlock()
	}()

	select {
	case <-time.After(s.wait):
	case <-ctx.Done():
		return nil, ctx.Err() //nolint:wrapcheck // a fixture
	}

	return s.LayerSource.Fetch(ctx, ids) //nolint:wrapcheck // a fixture
}

// windows is when each fetch happened.
func (s *slowFetch) windows() []window {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]window(nil), s.when...)
}

// timed runs a step and records when it did.
type timed struct {
	hold time.Duration

	mu   sync.Mutex
	when []window
}

func (w *timed) Run(
	ctx context.Context, _ *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	began := time.Now()

	select {
	case <-time.After(w.hold):
	case <-ctx.Done():
	}

	w.mu.Lock()
	w.when = append(w.when, window{from: began, to: time.Now()})
	w.mu.Unlock()

	return core.Result{}, nil
}

// windows is when each step ran.
func (w *timed) windows() []window {
	w.mu.Lock()
	defer w.mu.Unlock()

	return append([]window(nil), w.when...)
}

// A queued step fetches its inputs while the machine is busy with another.
//
// Transfer and compute are the two costs a delegated step has, and on a worker
// with room for one they were strictly serial: the step waited for a slot,
// *then* went looking for its base. A machine with a step to run and a step to
// fetch for did the fetching only once the running was done, which is the one
// arrangement where a fast network buys nothing.
//
// Two steps, one slot, a 300ms fetch and a 300ms compute each. Serial is 1200ms;
// overlapped is about 900 - the second step's transfer happening inside the
// first step's compute (E275).
func TestAQueuedStepFetchesWhileTheMachineIsBusy(t *testing.T) {
	t.Parallel()

	const (
		fetch   = 300 * time.Millisecond
		compute = 300 * time.Millisecond
	)

	remote := newMapStore()
	first := putBlob(t, remote, []byte("one"))
	second := putBlob(t, remote, []byte("two"))

	src := &slowFetch{
		LayerSource: &fleet.LayerSource{Held: remote},
		wait:        fetch,
	}

	steps := &timed{hold: compute}

	run := fleet.Runner(steps, core.Worker{ID: "w1"},
		fleet.WithCapacity(1), fleet.WithBlobs(newMapStore(), src))

	var wg sync.WaitGroup

	for _, id := range []ir.NodeID{first, second} {
		wg.Go(func() {
			_, _ = run(t.Context(), fleet.Assignment{
				Version: fleet.Version,
				Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
				Base:    []ir.NodeID{id},
			})
		})
	}

	wg.Wait()

	// **A transfer that happened while a step was running.**
	//
	// The claim with no threshold in it: the worker either did one step's
	// transfer during another's compute or it did not. On a serial worker the
	// second step's fetch begins when the first step's compute has finished,
	// and no two windows meet.
	//
	// This compared the whole run against `fetch + 2*compute + fetch/2` -
	// 1050ms of bar for 900ms of work, so 150ms of slack for two goroutines,
	// six sleeps and a store. It passed alone and failed inside the whole-suite
	// run, the third test here found measuring the machine rather than the
	// engine (E473, E481).
	var overlapped bool

	for _, f := range src.windows() {
		for _, r := range steps.windows() {
			if f.overlaps(r) {
				overlapped = true
			}
		}
	}

	if !overlapped {
		t.Errorf("no transfer happened while a step was running"+
			"\n  fetches %v\n  steps %v"+
			"\n  a worker with something to run and something to fetch for did"+
			" the fetching only once the running was done (E275)",
			src.windows(), steps.windows())
	}
}

// asking runs a step and looks at the world while it does.
//
// A fixture rather than a clock: what this file is about is two things
// happening at once, and "at once" is a question something inside one of them
// can answer directly.
type asking struct {
	hold time.Duration
	ask  func()
}

func (a *asking) Run(
	ctx context.Context, _ *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	a.ask()

	select {
	case <-time.After(a.hold):
	case <-ctx.Done():
	}

	// Asked again on the way out: the queued step's fetch may start after this
	// one began, and either sighting is the overlap.
	a.ask()

	return core.Result{}, nil
}
