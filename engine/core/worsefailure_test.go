package core

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// A cancellation never outranks the failure that caused it.
//
// The scheduler reports the *earliest failure in graph order*, so a build blames
// the same command whichever goroutine loses the race. That rule is right for
// two genuine failures and wrong for the pair it actually sees most often: one
// real failure, and the cancellation it triggered in a step that was still
// running.
//
// A cancellation is not a failure. It is a consequence, and reporting it in
// place of its own cause gives the author `context canceled` for a build that
// failed because a command exited 3.
//
// *Failure class: the consequence shadowing its cause.* It surfaced by adding a
// field to `ir.Op`: every node identity changed, the graph order changed with
// it, and a test that had been hardened against exactly this (E193) went red for
// a third reason nobody had named.
func TestACancellationNeverOutranksItsCause(t *testing.T) {
	t.Parallel()

	actual := errors.New("run doomed: exit status 3")
	cancelled := fmt.Errorf("run sibling: %w", context.Canceled)

	for _, tc := range []struct {
		name            string
		cur             error
		curAt           int
		next            error
		nextAt          int
		want            error
		wantAtIsNextsAt bool
	}{
		{"the cause arrives first, later in order", actual, 9, cancelled, 1, actual, false},
		{"the cancellation arrives first, earlier in order", cancelled, 1, actual, 9, actual, true},
		{
			"two actual failures, earliest in order wins", actual, 9,
			errors.New("run other: exit status 1"), 1, nil, true,
		},
		{
			"two cancellations, earliest in order wins", cancelled, 9,
			fmt.Errorf("run third: %w", context.Canceled), 1, nil, true,
		},
		{"nothing yet", nil, 1 << 30, cancelled, 4, cancelled, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			at, got := worseFailure(tc.cur, tc.curAt, tc.next, tc.nextAt)

			if tc.want != nil && !errors.Is(got, tc.want) {
				t.Errorf("reported %v, want %v", got, tc.want)
			}

			if tc.wantAtIsNextsAt && at != tc.nextAt {
				t.Errorf("kept index %d, want %d - the index must travel with the"+
					" error it belongs to", at, tc.nextAt)
			}

			if !tc.wantAtIsNextsAt && at != tc.curAt {
				t.Errorf("moved to index %d, want %d", at, tc.curAt)
			}
		})
	}
}
