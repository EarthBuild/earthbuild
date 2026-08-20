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

// A worker says how long a step waited for a slot.
//
// **Overhead was one number covering two things.** The driver computes it by
// subtracting what a worker reports from the round trip, and a worker reports
// its transfer and its step - so the wait for a slot, which is neither, lands in
// the same bucket as the network. Measured at four workers the total was a fixed
// 500ms a step (E335), and there was no way to tell a busy fleet from a slow one.
//
// A queue is not waste: a worker with more steps than slots is a worker being
// used. Network time on the same steps *is* waste. Reporting them as one number
// means the account cannot say which of the two a fleet has.
func TestAWorkerSaysHowLongAStepWaitedForASlot(t *testing.T) {
	t.Parallel()

	// The second step is sent only once the first is *inside* the executor, so
	// it must queue. Two goroutines racing to send was the first version, and it
	// depended on both arriving before either finished - which is true on an
	// idle machine and not on a loaded one, where the whole-suite run had the
	// second arrive after the first was done and nothing queued at all (E481).
	//
	// The same correction as E473's cache-claim test, one layer up: there a
	// fixed bar measured the machine, here a fixed *ordering* assumed it.
	started := make(chan struct{})
	step := &slowExec{took: 150 * time.Millisecond, started: started}

	run := fleet.Runner(step, core.Worker{ID: "w"}, fleet.WithCapacity(1))

	type outcome struct {
		wait int64
		err  error
	}

	first := make(chan outcome, 1)

	go func() {
		reply, err := run(t.Context(), anAssignment())
		first <- outcome{wait: reply.QueueMillis, err: err}
	}()

	<-started

	reply, err := run(t.Context(), anAssignment())
	if err != nil {
		t.Fatalf("the queued step: %v", err)
	}

	head := <-first
	if head.err != nil {
		t.Fatalf("the first step: %v", head.err)
	}

	// The bar is a third of the step rather than a number: what is asserted is
	// that one waited for the *other*, and the other's length is what that is
	// relative to.
	bar := (150 * time.Millisecond / 3).Milliseconds()

	if head.wait > bar {
		t.Errorf("the first step reported waiting %dms for a slot nothing else"+
			" held", head.wait)
	}

	if reply.QueueMillis <= bar {
		t.Errorf("the second step reported waiting %dms on a one-slot worker"+
			" already running a %v step"+
			"\n  a queue counted as network time makes a busy fleet look like"+
			" a slow one (E336)", reply.QueueMillis, 150*time.Millisecond)
	}
}

// anAssignment is one exec, which is all these tests need to place.
func anAssignment() fleet.Assignment {
	return fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
	}
}

type slowExec struct {
	took time.Duration
	// started is closed by the first step to enter, so a caller can send a
	// second one knowing the slot is taken.
	started chan struct{}
	once    sync.Once
}

func (s *slowExec) Run(
	ctx context.Context, _ *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	if s.started != nil {
		s.once.Do(func() { close(s.started) })
	}

	select {
	case <-time.After(s.took):
	case <-ctx.Done():
	}

	return core.Result{}, nil
}
