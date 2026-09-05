package fleet_test

import (
	"bytes"
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step that needs nothing does not queue behind a transfer.
//
// **Half a second per delegated step, measured.** With four workers the overhead
// was 525ms a step at 200ms of compute and 490ms at 1s - fixed, not proportional,
// which is the signature of a queue rather than of work. `provision` says why in
// its own comment: a worker has one uplink, so transfers are serialised, and
// *the cheap check happens outside the lock*.
//
// It did for whole layers and not for fragments. The lazy path added in E323
// took the mutex unconditionally, so every step after the first on a worker
// waited for a fetch it had no use for - the exact waste that comment describes,
// reintroduced beside it (E335).
func TestAStepThatNeedsNothingDoesNotQueueBehindATransfer(t *testing.T) {
	t.Parallel()

	held := layerStore(t)
	id := seedLayer(t, held, 3)

	want := []string{"usr/lib/lib0.so"}

	// A worker that already holds exactly this fragment.
	frags := &fleet.Fragments{Root: t.TempDir()}

	manifest, packed, err := held.Fragment(id, want)
	if err != nil {
		t.Fatalf("%v", err)
	}

	err = frags.PutVerified(id, want, manifest, bytes.NewReader(packed))
	if err != nil {
		t.Fatalf("%v", err)
	}

	// And a peer that never answers, for a fragment of something else.
	stuck := make(chan struct{})

	run := fleet.Runner(&countingExecutor{}, core.Worker{ID: "w"},
		fleet.WithCapacity(4),
		fleet.WithFragments(frags, &stuckFragments{until: stuck, from: held}))

	other := seedLayer(t, held, 2)

	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = run(t.Context(), assignmentOn(other, want))
	})

	// Give the blocked fetch time to take the lock.
	time.Sleep(50 * time.Millisecond)

	done := make(chan struct{})

	go func() {
		defer close(done)

		_, _ = run(t.Context(), assignmentOn(id, want))
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("a step whose fragment was already here waited for a transfer" +
			" it had no use for\n  half a second a step, at four workers" +
			" (E335)")
	}

	// **Released before waiting**, not in a Cleanup: the blocked fetch holds
	// the worker's transfer lock, so a test that waits for it first waits for
	// ever - which is how the first version of this failed, by timing out
	// rather than by failing.
	close(stuck)
	wg.Wait()
	<-done
}

func assignmentOn(id ir.NodeID, want []string) fleet.Assignment {
	return fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
		Base:    []ir.NodeID{id},
		Hints:   fleet.Hints{ReadsPredicted: want},
	}
}

// stuckFragments answers nothing until it is released.
type stuckFragments struct {
	until chan struct{}
	from  *fleet.Layers
}

func (s *stuckFragments) Fragment(
	ctx context.Context, id ir.NodeID, want []string, proof bool,
) (manifest, packed []byte, err error) {
	select {
	case <-s.until:
	case <-ctx.Done():
	}

	manifest, packed, err = s.from.Fragment(id, want)
	if !proof {
		manifest = nil
	}

	return manifest, packed, err
}

// Waiting for the uplink is counted as transfer, not as nothing.
//
// **506ms a step of "wire" that was not the wire** (E336). A worker serialises
// transfers - it has one uplink - and `Provision` starts its clock *after* the
// lock, so a step queued behind another machine's fetch reported no transfer
// time at all. The driver computes the wire by subtracting what a worker
// reports from the round trip, so every second of that queue was attributed to
// the network.
//
// The account then said the fleet was overhead-bound when it was transfer-bound,
// which are different problems with different fixes: one says the protocol is
// expensive, the other says the uplink is.
func TestWaitingForTheUplinkIsCountedAsTransfer(t *testing.T) {
	t.Parallel()

	held := layerStore(t)
	first := seedLayer(t, held, 3)
	second := seedLayer(t, held, 2)

	want := []string{"usr/lib/lib0.so"}

	// The second step is started only once the first is *inside* the fetch, so
	// the contention this measures is not a race with the scheduler. Before
	// E338 the fetch was slow enough that overlapping happened by luck; once it
	// was not, the test failed on a loaded machine.
	inside := make(chan struct{}, 1)

	// **A wide window on purpose.** The second step is started once the first is
	// inside the fetch, but starting a goroutine is not reaching the lock: under
	// a loaded machine it can take longer than a short fetch lasts, and then
	// there is no contention to measure and the test passes having measured
	// nothing. It failed that way in a full suite run and not once in eight
	// alone.
	slow := &slowFragments{delay: 500 * time.Millisecond, from: held, inside: inside}

	run := fleet.Runner(&countingExecutor{}, core.Worker{ID: "w"},
		fleet.WithCapacity(4),
		fleet.WithFragments(&fleet.Fragments{Root: t.TempDir()}, slow))

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		spent []int64
	)

	for i, id := range []ir.NodeID{first, second} {
		if i == 1 {
			<-inside
		}
		wg.Go(func() {
			reply, err := run(t.Context(), assignmentOn(id, want))
			if err != nil {
				t.Errorf("%v", err)

				return
			}

			mu.Lock()
			spent = append(spent, reply.FetchMillis)
			mu.Unlock()
		})
	}

	wg.Wait()

	// Compared against each other, not against a constant.
	//
	// This counted how many steps exceeded 700 ms and demanded exactly one. The
	// property is that the *second* step waits for the first, and 700 ms was a
	// stand-in for "waited" measured on an idle machine - so under load the step
	// that did *not* queue crossed it too, the count came to two, and the test
	// failed about something it was not testing (E416).
	//
	// The pair carries the answer: one of them queued behind the other, so one
	// is markedly slower. How slow either is in absolute terms is a fact about
	// the machine.
	slower, faster := slices.Max(spent), slices.Min(spent)

	if slower < faster*3/2 {
		t.Errorf("neither step waited noticeably for the other: %v"+
			"\n  one queues behind the other on a single uplink, and the one that"+
			" waited must report that time as transfer or the driver calls it"+
			" network (E336)", spent)
	}
}

// slowFragments takes a stated time to answer.
type slowFragments struct {
	delay  time.Duration
	from   *fleet.Layers
	inside chan struct{}
}

func (s *slowFragments) Fragment(
	ctx context.Context, id ir.NodeID, want []string, proof bool,
) (manifest, packed []byte, err error) {
	if s.inside != nil {
		select {
		case s.inside <- struct{}{}:
		default:
		}
	}

	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
	}

	manifest, packed, err = s.from.Fragment(id, want)
	if !proof {
		manifest = nil
	}

	return manifest, packed, err
}
