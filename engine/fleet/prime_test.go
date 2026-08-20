package fleet_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// An assignment with no operation is a request to be ready.
//
// **The remaining cost has a shape** (E341): one fetch per worker per build,
// about 300ms, paid at the moment the first step arrives - while three other
// machines have nothing to do. Making the fetch faster is the wrong lever; the
// fetch should already have happened.
//
// A prime is an assignment stripped of its step: the same base, the same
// prediction, the same provisioning, and nothing run. It needs no second message
// type, no second path through a worker, and a worker that does not understand
// it refuses exactly as it refuses any operation it does not know (I10) - which
// costs the build nothing, because a prime is advice about *when*, not about
// what.
func TestAnAssignmentWithNoOperationIsARequestToBeReady(t *testing.T) {
	t.Parallel()

	held := layerStore(t)
	id := seedLayer(t, held, 3)

	into := &fleet.Fragments{Root: t.TempDir()}
	ran := &countingExecutor{}

	run := fleet.Runner(ran, core.Worker{ID: "w"},
		fleet.WithFragments(into, localFragments{from: held}))

	want := []string{"usr/lib/lib1.so"}

	reply, err := run(t.Context(), fleet.Assignment{
		Version: fleet.Version,
		Base:    []ir.NodeID{id},
		Hints:   fleet.Hints{ReadsPredicted: want},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}

	if reply.Refused != "" {
		t.Fatalf("a prime was refused: %s", reply.Refused)
	}

	if !into.Has(id, want) {
		t.Error("a prime moved nothing, so the step it was for will still wait")
	}

	if ran.runs != 0 {
		t.Errorf("a prime ran %d step(s); it names none", ran.runs)
	}

	if reply.Layer != (ir.NodeID{}) {
		t.Error("a prime produced a layer, which a request to be ready cannot")
	}

	// It still says what it cost, because that is what the account is for and a
	// prime is where a fleet spends its first second.
	if reply.FetchMillis == 0 && reply.FetchedBytes == 0 {
		t.Error("a prime reported moving nothing at all")
	}
}

// Every worker is told what the build stands on, before any of them is asked.
//
// **Three machines idle while one fetches** (E341). A prime sent only to the
// worker being assigned would move the cost, not remove it: the second worker
// pays it when the second step arrives, the third when the third does, and the
// fleet warms up one machine at a time.
//
// Once per build, not once per step: the base of a build is the same for every
// step that stands on it, and a driver that primed repeatedly would spend its
// own uplink telling machines what they already have.
func TestEveryWorkerIsToldWhatTheBuildStandsOn(t *testing.T) {
	t.Parallel()

	base := ir.NodeID{7}

	fleet2 := &primingTransport{reply: fleet.Reply{
		Version: fleet.Version, Layer: ir.NodeID{8},
	}}

	d := &fleet.Delegating{
		Local:   refusing{},
		Fleet:   fleet2,
		Predict: func(*ir.Node) []string { return []string{"usr/lib/a.so"} },
	}

	for range 3 {
		if _, err := d.Run(t.Context(), execNode(), core.Worker{ID: "w"},
			[]ir.NodeID{base}, nil); err != nil {
			t.Fatalf("%v", err)
		}
	}

	if fleet2.primes != 1 {
		t.Errorf("%d prime(s) for three steps on one base, want 1"+
			"\n  a driver that primes per step spends its uplink saying what"+
			" every worker already has (E342)", fleet2.primes)
	}

	if got := fleet2.primed.Base; len(got) != 1 || got[0] != base {
		t.Errorf("the prime named %v, not the base the steps stand on", got)
	}

	if len(fleet2.primed.Hints.ReadsPredicted) == 0 {
		t.Error("the prime carried no prediction, so a worker would fetch the" +
			" whole base to be ready for part of it")
	}
}

// primingTransport records primes separately from assignments.
type primingTransport struct {
	mu     sync.Mutex
	primes int
	primed fleet.Assignment
	asked  int
	reply  fleet.Reply
}

func (p *primingTransport) Assign(
	_ context.Context, a fleet.Assignment,
) (fleet.Reply, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.asked++

	return p.reply, nil
}

func (p *primingTransport) PrimeAll(_ context.Context, a fleet.Assignment) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.primes++
	p.primed = a
}

func (p *primingTransport) Workers() int { return 4 }

// A prime goes to every worker at once, and the build does not wait for it.
//
// Two properties, and the second is why it helps at all: a prime that blocked
// the first assignment would pay the transfer before the build rather than
// during it, which is the same second spent in a different order.
func TestAPrimeReachesEveryWorkerWithoutBlocking(t *testing.T) {
	t.Parallel()

	var r fleet.Rendezvous

	for range 3 {
		r.AddForTest()
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		r.PrimeAll(t.Context(), fleet.Assignment{
			Version: fleet.Version,
			Base:    []ir.NodeID{{1}},
		})
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("priming a fleet blocked, so the build waits for what the" +
			" prime exists to overlap with (E342)")
	}
}
