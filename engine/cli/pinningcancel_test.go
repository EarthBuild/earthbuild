package cli

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

// **A lookup that was abandoned did not fail to pin.**
//
// The prefetch resolver runs ahead of the walk, so a command that answers
// without building - `--dry-run`, `check-inputs` - returns while a round trip is
// still in flight and cancels it. Reporting that as "was not pinned" tells the
// reader the build's keys are coarser than they are, beside an answer that is
// exactly right: the one place a false note is worse than none.
func TestACancelledLookupIsNotReportedAsUnpinned(t *testing.T) {
	t.Parallel()

	for _, one := range []struct {
		name string
		err  error
		said bool
	}{
		{"cancelled", context.Canceled, false},
		{"timed out", context.DeadlineExceeded, false},
		{"wrapped cancellation", errors.Join(errors.New("fetch"), context.Canceled), false},
		{"a real failure", errors.New("no such image"), true},
	} {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			notePinFailure(&out, "alpine:3.22", one.err)

			if said := out.Len() > 0; said != one.said {
				t.Errorf("%v printed %q", one.err, out.String())
			}
		})
	}
}
