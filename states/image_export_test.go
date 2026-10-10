package states

import (
	"context"
	"errors"
	"sync"
	"testing"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

// Only one exporter ever gets an image's export, however many ask and in
// whichever order.
func TestImageExportHasOneExporter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		waitBlockFirst    bool
		wantWaitBlockWins bool
	}{
		{name: "wait block first", waitBlockFirst: true, wantWaitBlockWins: true},
		{name: "builder.go first"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := &ImageExport{}

			if tt.waitBlockFirst {
				if !e.TakeForWaitBlock() {
					t.Fatal("the first exporter must get the export")
				}
			} else if !e.TakeForBuilder() {
				t.Fatal("the first exporter must get the export")
			}

			if got := e.TakeForWaitBlock(); got != tt.wantWaitBlockWins {
				t.Errorf("TakeForWaitBlock() = %v, want %v", got, tt.wantWaitBlockWins)
			}

			if got := e.TakeForBuilder(); got == tt.wantWaitBlockWins {
				t.Errorf("TakeForBuilder() = %v, want %v", got, !tt.wantWaitBlockWins)
			}

			if got := e.TakenByWaitBlock(); got != tt.wantWaitBlockWins {
				t.Errorf("TakenByWaitBlock() = %v, want %v", got, tt.wantWaitBlockWins)
			}
		})
	}
}

func TestImageExportConcurrentTakesAgree(t *testing.T) {
	t.Parallel()

	e := &ImageExport{}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)

	for i := range 16 {
		wg.Go(func() {
			take := e.TakeForBuilder
			if i%2 == 0 {
				take = e.TakeForWaitBlock
			}

			if take() {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		})
	}

	wg.Wait()

	// Every caller of the winning kind wins, and no caller of the other.
	if wins != 8 {
		t.Errorf("%d callers got the export, want the 8 of the first exporter's kind", wins)
	}
}

func TestExportOutcomeWait(t *testing.T) {
	t.Parallel()

	want := errors.New("push failed")

	var o ExportOutcome

	go o.Settle(t.Context(), want)

	got := o.Wait(t.Context())
	if !errors.Is(got, want) {
		t.Errorf("Wait() = %v, want %v", got, want)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var pending ExportOutcome

	got = pending.Wait(ctx)
	if !errors.Is(got, context.Canceled) {
		t.Errorf("Wait() on a cancelled context = %v, want %v", got, context.Canceled)
	}
}

// An image's state is solved once, however many exports ask for it at the same
// time; a failed solve is not kept.
func TestImageExportRefSolvesOnce(t *testing.T) {
	t.Parallel()

	var (
		e      ImageExport
		mu     sync.Mutex
		solves int
		wg     sync.WaitGroup
	)

	failed := errors.New("solve failed")

	_, err := e.Ref(t.Context(), func(context.Context) (gwclient.Reference, error) {
		return nil, failed
	})
	if !errors.Is(err, failed) {
		t.Fatalf("Ref() = %v, want %v", err, failed)
	}

	for range 8 {
		wg.Go(func() {
			_, refErr := e.Ref(t.Context(), func(context.Context) (gwclient.Reference, error) {
				mu.Lock()
				solves++
				mu.Unlock()

				return nil, nil
			})
			if refErr != nil {
				t.Errorf("Ref() = %v, want nil", refErr)
			}
		})
	}

	wg.Wait()

	if solves != 1 {
		t.Errorf("the state was solved %d times after the failed solve, want 1", solves)
	}

	done, err := e.Pushed.Result()
	if done || err != nil {
		t.Errorf("Pushed.Result() = %v, %v, want false, nil before it is settled", done, err)
	}
}
