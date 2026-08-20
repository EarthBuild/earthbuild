package fleet_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// watching counts how many steps are inside the executor at once.
type watching struct {
	now, most atomic.Int64
	hold      time.Duration
}

func (w *watching) Run(
	context.Context, *ir.Node, core.Worker, []ir.NodeID, [][]ir.NodeID,
) (core.Result, error) {
	n := w.now.Add(1)
	for {
		m := w.most.Load()
		if n <= m || w.most.CompareAndSwap(m, n) {
			break
		}
	}

	time.Sleep(w.hold)
	w.now.Add(-1)

	return core.Result{Layer: ir.NodeID{1}}, nil
}

// A worker runs no more steps at once than it has room for.
//
// Found by trying to measure a speedup and failing to get one: **one worker ran
// six steps of 250ms in 267ms**, because a worker had no notion of capacity at
// all and started every assignment the moment it arrived. One machine was
// therefore infinitely parallel, and no number of machines could beat it (E271).
//
// It is not only a benchmarking problem. A real machine has cores; a worker that
// starts fifty steps on eight of them thrashes, and the driver's load model -
// which decides whether a holder is still the cheapest place (E266) - is
// meaningless when "busy" never costs anything.
func TestAWorkerRunsNoMoreStepsAtOnceThanItHasRoomFor(t *testing.T) {
	t.Parallel()

	const (
		room  = 2
		steps = 8
	)

	x := &watching{hold: 40 * time.Millisecond}

	run := fleet.Runner(x, core.Worker{ID: "w1"}, fleet.WithCapacity(room))

	var wg sync.WaitGroup

	for range steps {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, _ = run(t.Context(), fleet.Assignment{
				Version: fleet.Version,
				Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
			})
		}()
	}

	wg.Wait()

	if got := x.most.Load(); got > room {
		t.Errorf("%d steps ran at once on a worker with room for %d"+
			"\n  a machine has cores, and a worker that ignores them thrashes"+
			" while telling the driver it is not busy", got, room)
	}

	if x.most.Load() < 2 {
		t.Error("nothing overlapped at all; a worker with room for two should" +
			" use it")
	}
}

// Every step still runs, however long it has to wait.
//
// A capacity is a queue, not a refusal. Turning work away because the machine is
// busy would send the driver looking for somewhere else while this machine is
// about to be free - and on a fleet where every worker is busy, that is a build
// that fails for being popular.
func TestAWorkerAtCapacityQueuesRatherThanRefuses(t *testing.T) {
	t.Parallel()

	x := &watching{hold: 5 * time.Millisecond}

	run := fleet.Runner(x, core.Worker{ID: "w1"}, fleet.WithCapacity(1))

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		refs int
		done int
	)

	for range 6 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			reply, err := run(t.Context(), fleet.Assignment{
				Version: fleet.Version,
				Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
			})

			mu.Lock()
			defer mu.Unlock()

			if err == nil && reply.Refused == "" {
				done++
			} else if reply.Refused != "" {
				refs++
			}
		}()
	}

	wg.Wait()

	if refs != 0 {
		t.Errorf("%d step(s) were refused for want of room"+
			"\n  a fleet where everybody is busy would fail a build for being"+
			" popular", refs)
	}

	if done != 6 {
		t.Errorf("%d of 6 steps completed", done)
	}
}

// A worker with no capacity configured has as much as the machine has cores.
//
// The default has to be a number rather than "unlimited", because unlimited is
// what produced a worker that was infinitely parallel - and it has to come from
// the machine, because the driver cannot know and a fixed guess is wrong on
// every machine but one.
func TestAWorkerDefaultsToTheMachinesCores(t *testing.T) {
	t.Parallel()

	x := &watching{hold: 20 * time.Millisecond}

	run := fleet.Runner(x, core.Worker{ID: "w1"})

	var wg sync.WaitGroup

	for range 64 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, _ = run(t.Context(), fleet.Assignment{
				Version: fleet.Version,
				Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
			})
		}()
	}

	wg.Wait()

	if got := x.most.Load(); got > int64(fleet.DefaultCapacity()) {
		t.Errorf("%d steps ran at once with a default capacity of %d",
			got, fleet.DefaultCapacity())
	}
}

// A worker says how big it is, in every reply.
//
// The driver has no other way to learn the denominator it balances on (E272),
// and a fleet whose workers never announced it would be balanced as though every
// machine had one core - which is the arrangement that gives a sixty-four core
// machine the same share as a laptop.
func TestAWorkerSaysHowBigItIs(t *testing.T) {
	t.Parallel()

	run := fleet.Runner(&watching{}, core.Worker{ID: "w1"}, fleet.WithCapacity(6))

	reply, err := run(t.Context(), fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}

	if reply.Capacity != 6 {
		t.Errorf("the reply says room for %d, want 6", reply.Capacity)
	}
}
