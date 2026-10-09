package states

import (
	"context"
	"errors"
	"testing"
)

func TestPendingExportsAbort(t *testing.T) {
	t.Parallel()

	cause := errors.New("conversion failed")

	var (
		pending          PendingExports
		settled, waiting ExportOutcome
		gotSettled       []error
		gotWaiting       []error
		gotLate          []error
	)

	pending.Then(t.Context(), &settled, func(_ context.Context, err error) { gotSettled = append(gotSettled, err) })
	pending.Then(t.Context(), &waiting, func(_ context.Context, err error) { gotWaiting = append(gotWaiting, err) })
	settled.Settle(t.Context(), nil)

	pending.Abort(t.Context(), cause)
	pending.Abort(t.Context(), errors.New("second abort"))
	waiting.Settle(t.Context(), nil) // a late export changes nothing

	var late ExportOutcome

	pending.Then(t.Context(), &late, func(_ context.Context, err error) { gotLate = append(gotLate, err) })

	if len(gotSettled) != 1 || gotSettled[0] != nil {
		t.Errorf("an outcome settled before Abort got %v, want one nil", gotSettled)
	}

	for name, got := range map[string][]error{"pending": gotWaiting, "kept after Abort": gotLate} {
		if len(got) != 1 {
			t.Fatalf("%s: called %d times, want once", name, len(got))
		}

		if !errors.Is(got[0], context.Canceled) || !errors.Is(got[0], cause) {
			t.Errorf("%s: got %v, want an error wrapping context.Canceled and %v", name, got[0], cause)
		}
	}
}

func TestNilPendingExportsKeepsNothing(t *testing.T) {
	t.Parallel()

	var (
		pending *PendingExports
		outcome ExportOutcome
		calls   int
	)

	pending.Then(t.Context(), &outcome, func(context.Context, error) { calls++ })
	pending.Abort(t.Context(), errors.New("ignored"))

	if calls != 0 {
		t.Fatalf("a nil PendingExports settled an outcome")
	}

	outcome.Settle(t.Context(), nil)

	if calls != 1 {
		t.Errorf("called %d times, want once", calls)
	}
}
