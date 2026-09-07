package fleet_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

func step() fleet.Assignment {
	return fleet.Assignment{
		Version: fleet.Version,
		Base:    []ir.NodeID{{1}},
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
	}
}

// The three protocols are separate, and each says what it is for.
//
// C.2 gives three ALPNs rather than one stream with message kinds, and C.4 says
// why: blobs move in batches, and a thousand-blob synchronisation must not be a
// thousand streams competing with the control traffic that decides what to fetch
// next. **A heartbeat behind a gigabyte of layer is a worker presumed dead.**
//
// Asserted because a protocol identifier is the one string in a system that
// cannot be changed unilaterally: both ends have to agree, and a typo is a fleet
// that silently never forms.
func TestTheProtocolsAreTheOnesTheSpecificationNames(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ got, want string }{
		{fleet.ALPNControl, "earth/ctl/1"},
		{fleet.ALPNBlob, "earth/blob/1"},
		{fleet.ALPNMask, "earth/mask/1"},
	} {
		if tc.got != tc.want {
			t.Errorf("the protocol is %q and C.2 names %q; both ends have to"+
				" agree and a typo is a fleet that never forms", tc.got, tc.want)
		}
	}

	// Distinct, which is what makes them separate streams rather than one with
	// three names.
	seen := map[string]bool{}
	for _, a := range []string{fleet.ALPNControl, fleet.ALPNBlob, fleet.ALPNMask} {
		if seen[a] {
			t.Errorf("%q is used for two protocols", a)
		}

		seen[a] = true
	}
}

// A step goes to exactly one worker.
func TestAnAssignmentReachesOneWorker(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		runs int
	)

	f := &fleet.InProcess{}

	for range 3 {
		f.AddWorker(func(context.Context, fleet.Assignment) (fleet.Reply, error) {
			mu.Lock()
			defer mu.Unlock()

			runs++

			return fleet.Reply{Version: fleet.Version, Layer: ir.NodeID{7}}, nil
		})
	}

	got, err := f.Assign(context.Background(), step())
	if err != nil {
		t.Fatal(err)
	}

	if got.Layer != (ir.NodeID{7}) {
		t.Errorf("the reply carried %v", got.Layer)
	}

	if runs != 1 {
		t.Errorf("%d workers ran the step; two workers running one step is not"+
			" wrong - steps are pure - but it is waste that grows with the"+
			" fleet", runs)
	}
}

// A worker that disappears mid-step costs a re-queue, not a build.
//
// C.5, and it is sound for the reason the specification gives: **steps are
// pure** (I1), so a step that vanished with its worker can be run again anywhere
// and the second attempt produces what the first would have. It is the same
// property that makes retry safe (I7).
//
// The distinction being tested is between a worker that stopped answering and a
// step that failed. Only the first may be re-queued; re-queueing the second
// would run a failing step on every machine in the fleet in turn.
func TestAWorkerThatDisappearsCostsAReQueue(t *testing.T) {
	t.Parallel()

	f := &fleet.InProcess{}

	// The first worker vanishes mid-step; the second answers.
	f.AddWorker(func(context.Context, fleet.Assignment) (fleet.Reply, error) {
		return fleet.Reply{}, fleet.ErrWorkerGone
	})

	f.AddWorker(func(context.Context, fleet.Assignment) (fleet.Reply, error) {
		return fleet.Reply{Version: fleet.Version, Layer: ir.NodeID{9}}, nil
	})

	got, err := f.Assign(context.Background(), step())
	if err != nil {
		t.Fatalf("a worker disappeared and the step failed: %v"+
			"\n  a pure step that lost its worker can be run anywhere", err)
	}

	if got.Layer != (ir.NodeID{9}) {
		t.Errorf("the re-queued step returned %v", got.Layer)
	}
}

// A step that fails is not re-queued.
func TestAFailingStepIsNotRunOnEveryMachineInTurn(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		runs int
	)

	boom := errors.New("the step could not be started")

	f := &fleet.InProcess{}

	for range 4 {
		f.AddWorker(func(context.Context, fleet.Assignment) (fleet.Reply, error) {
			mu.Lock()
			defer mu.Unlock()

			runs++

			return fleet.Reply{}, boom
		})
	}

	_, err := f.Assign(context.Background(), step())
	if !errors.Is(err, boom) {
		t.Errorf("the error was %v, want the worker's own", err)
	}

	if runs != 1 {
		t.Errorf("a failing step ran on %d machines; only a *disappearance* may"+
			" be re-queued, and a failure is an answer", runs)
	}
}

// A fleet where everybody has died says so, and says which.
func TestAFleetWithNoLiveWorkerSaysWhichKindOfNothing(t *testing.T) {
	t.Parallel()

	empty := &fleet.InProcess{}

	_, err := empty.Assign(context.Background(), step())
	if !errors.Is(err, fleet.ErrNoWorker) {
		t.Errorf("an empty fleet said %v, want ErrNoWorker", err)
	}

	// One that had a worker and lost it is a different situation: the step was
	// attempted and can be attempted again, which is what a driver deciding
	// whether to build locally needs to know.
	dying := &fleet.InProcess{}
	dying.AddWorker(func(context.Context, fleet.Assignment) (fleet.Reply, error) {
		return fleet.Reply{}, fleet.ErrWorkerGone
	})

	_, err = dying.Assign(context.Background(), step())
	if !errors.Is(err, fleet.ErrWorkerGone) {
		t.Errorf("a fleet whose only worker vanished said %v, want ErrWorkerGone", err)
	}

	if !strings.Contains(err.Error(), "1 attempt") {
		t.Errorf("the refusal does not say how many workers were tried: %v", err)
	}

	if dying.Workers() != 1 {
		t.Errorf("Workers() is %d; a worker that returned ErrWorkerGone has not"+
			" been marked dead, which is Kill's job and not Assign's",
			dying.Workers())
	}
}
