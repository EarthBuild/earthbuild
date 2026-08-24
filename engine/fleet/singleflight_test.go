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

// Concurrent steps needing one base fetch it once.
//
// Found by measuring rather than by reasoning: an eight-way fan-out over three
// workers moved five copies of a base where two would do. Steps run concurrently
// on a worker, and each of them looked, saw the base was absent, and fetched it -
// so the machine pulled the same hundreds of megabytes down its one uplink
// several times over.
//
// **A worker has one pipe.** Fetching twice at once does not halve the time, it
// halves the share, so provisioning is serialised per worker and the second step
// finds what the first brought. Steps that need nothing do not queue behind it -
// they are the common case once a fleet is warm, and making them wait for a
// transfer they have no use for would trade one waste for another.
func TestConcurrentStepsFetchOneBaseOnce(t *testing.T) {
	t.Parallel()

	const steps = 6

	body := make([]byte, 32<<10)

	driver := newMapStore()
	id := putBlob(t, driver, body)

	src := &countingSource{LayerSource: &fleet.LayerSource{Label: "driver", Held: driver}}

	run := fleet.Runner(&countingLocal{}, core.Worker{ID: "w1"},
		fleet.WithBlobs(newMapStore(), src))

	a := fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
		Base:    []ir.NodeID{id},
	}

	var wg sync.WaitGroup

	for range steps {
		wg.Go(func() {
			_, _ = run(t.Context(), a)
		})
	}

	wg.Wait()

	if src.batches != 1 {
		t.Errorf("%d steps needing one base opened %d fetch(es)"+
			"\n  a worker has one uplink; fetching the same base six times at"+
			" once does not make it arrive sooner", steps, src.batches)
	}
}

// A step that needs nothing does not wait behind a transfer.
//
// The other side of serialising transfers. Once a fleet is warm most steps need
// nothing at all, and making them queue behind somebody else's gigabyte would
// trade a bandwidth waste for a latency one - the fleet would look busy while
// every warm step sat waiting for a pipe it had no use for.
func TestAStepThatNeedsNothingDoesNotWaitForATransfer(t *testing.T) {
	t.Parallel()

	const slow = 400 * time.Millisecond

	body := []byte("something to fetch")

	remote := newMapStore()
	id := putBlob(t, remote, body)

	mine := newMapStore()

	// Already here, so the second step needs nothing.
	warm := []byte("already present")
	warmID := putBlob(t, mine, warm)

	run := fleet.Runner(&countingLocal{}, core.Worker{ID: "w1"},
		fleet.WithBlobs(mine, &slowSource{
			LayerSource: &fleet.LayerSource{Held: remote}, wait: slow,
		}))

	started := make(chan struct{})
	done := make(chan time.Duration, 1)

	go func() {
		close(started)

		_, _ = run(t.Context(), fleet.Assignment{
			Version: fleet.Version,
			Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"slow"}},
			Base:    []ir.NodeID{id},
		})
	}()

	<-started
	time.Sleep(20 * time.Millisecond)

	go func() {
		began := time.Now()

		_, _ = run(t.Context(), fleet.Assignment{
			Version: fleet.Version,
			Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"warm"}},
			Base:    []ir.NodeID{warmID},
		})

		done <- time.Since(began)
	}()

	select {
	case took := <-done:
		if took > slow/2 {
			t.Errorf("a step needing nothing took %v while a %v transfer was in"+
				" flight\n  warm steps are the common case; queueing them behind"+
				" a fetch they have no use for trades one waste for another",
				took, slow)
		}

	case <-time.After(slow * 3):
		t.Fatal("a step needing nothing never finished")
	}
}

// slowSource answers, eventually.
type slowSource struct {
	*fleet.LayerSource
	wait time.Duration
}

func (s *slowSource) Fetch(
	ctx context.Context, ids []ir.NodeID,
) (map[ir.NodeID]io.Reader, error) {
	select {
	case <-time.After(s.wait):
	case <-ctx.Done():
		return nil, ctx.Err() //nolint:wrapcheck // a fixture
	}

	return s.LayerSource.Fetch(ctx, ids)
}
