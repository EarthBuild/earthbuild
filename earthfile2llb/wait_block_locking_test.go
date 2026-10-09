package earthfile2llb

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/states"
	"github.com/EarthBuild/earthbuild/util/llbutil/pllb"
	"github.com/EarthBuild/earthbuild/util/syncutil/semutil"
	"github.com/stretchr/testify/require"
)

// FinalizeStates' async goroutine acquires a slot of the shared Parallelism
// semaphore and then calls Converter.isStateExported, which takes wb.mu for
// every wait block on the stack. waitBlock.Wait holds wb.mu for its entire
// solve and export. So, while a wait block is being waited on, every target
// whose stack contains it parks a parallelism slot doing nothing.
// waitStates' own MultiSem fallback (an extra weight-1 semaphore) keeps this
// from deadlocking, but it means the wait block's states are then forced one at
// a time while the blocked goroutines hold every shared slot.
//
// This test holds wb.mu (as Wait does) and checks that the goroutine gives its
// parallelism slot back instead of parking on the lock with it.
func TestFinalizeStatesDoesNotHoldParallelismWhileWaitBlockBusy(t *testing.T) {
	t.Parallel()

	parallelism := newRecordingSemaphore(1)
	wb := newWaitBlock()

	c, eg := newFinalizeTestConverter(t, finalizeTestOpt{
		gw:          &fakeGwClient{},
		parallelism: parallelism,
		waitBlock:   wb,
		waitBlockOn: true,
		export:      ExportAll,
	})
	c.mts.Final.MainState = pllb.Image("alpine:3.20")

	_, err := c.FinalizeStates(t.Context())
	require.NoError(t, err)

	// A waitBlock.Wait is now in progress on wb. FinalizeStates has already
	// added its stateWaitItem, so taking the lock here does not block it.
	wb.mu.Lock()

	select {
	case <-parallelism.acquired:
	case <-time.After(5 * time.Second):
		wb.mu.Unlock()
		t.Fatal("FinalizeStates never acquired a parallelism slot")
	}

	released := false

	select {
	case <-parallelism.released:
		released = true
	case <-time.After(2 * time.Second):
	}

	wb.mu.Unlock()
	require.NoError(t, eg.Wait())

	require.True(t, released,
		"FinalizeStates held a parallelism slot for 2s while blocked on a busy wait block's mutex")
}

// recordingSemaphore is a semutil.Semaphore that reports each successful
// acquire and each release, so a test can tell whether a slot is being held.
type recordingSemaphore struct {
	semutil.Semaphore

	acquired chan struct{}
	released chan struct{}
}

func newRecordingSemaphore(n int64) *recordingSemaphore {
	return &recordingSemaphore{
		Semaphore: semutil.NewWeighted(n),
		acquired:  make(chan struct{}, 16),
		released:  make(chan struct{}, 16),
	}
}

func (s *recordingSemaphore) Acquire(ctx context.Context, n int64) (semutil.ReleaseFun, error) {
	rel, err := s.Semaphore.Acquire(ctx, n)
	if err != nil {
		return nil, err
	}

	s.acquired <- struct{}{}

	return func() {
		rel()

		s.released <- struct{}{}
	}, nil
}

// isStateExportedUnlocked reads saveImageWaitItem.doPush and .localExport under
// wb.mu only. Those fields are written under siwi.mu, and SingleTarget.SetDoSaves
// / SetDoPushes reach them through sts.WaitItems without taking wb.mu. The PR
// calls isStateExported from FinalizeStates' goroutines while other targets are
// still being converted and BUILD edges are still flipping these flags.
//
// This test only fails under the race detector:
//
//	go test -race ./earthfile2llb -run TestWaitBlockIsStateExportedDoesNotRaceSetDoSave
func TestWaitBlockIsStateExportedDoesNotRaceSetDoSave(t *testing.T) {
	t.Parallel()

	state := pllb.Image("alpine:3.20")
	c := &Converter{opt: ConvertOpt{Export: ExportAll}}

	sts := &states.SingleTarget{}
	wb := newWaitBlock()

	item := newSaveImage(states.SaveImage{DockerTag: testDockerTag, State: state}, c, true, false)
	wb.AddItem(item)
	sts.WaitItems = append(sts.WaitItems, item)

	var wg sync.WaitGroup

	wg.Go(func() {
		// A BUILD edge reaching an already-converted target.
		sts.SetDoSaves()
		sts.SetDoPushes()
	})

	wg.Go(func() {
		// FinalizeStates' goroutine for another target sharing wb.
		_ = wb.isStateExported(&state)
	})

	wg.Wait()
}
