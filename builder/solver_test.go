package builder

import (
	"context"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestChooseSolveError(t *testing.T) {
	t.Parallel()

	const dupMsg = "duplicate command ID"

	realErr := errors.New("failed to create command: " + dupMsg)
	canceled := context.Canceled

	tests := map[string]struct {
		buildErr   error
		monitorErr error
		wantSubstr string
		wantNil    bool
	}{
		"both nil": {
			wantNil: true,
		},
		"build error only": {
			buildErr:   realErr,
			wantSubstr: dupMsg,
		},
		"monitor error only": {
			monitorErr: realErr,
			wantSubstr: dupMsg,
		},
		"build canceled masks real monitor error": {
			// The bug: a real earth-side monitor failure cancels the build,
			// and the resulting bare cancellation must not win.
			buildErr:   canceled,
			monitorErr: realErr,
			wantSubstr: "earth progress monitor aborted the build",
		},
		"both canceled stays canceled": {
			buildErr:   canceled,
			monitorErr: context.Canceled,
			wantSubstr: context.Canceled.Error(),
		},
		"real build error beats canceled monitor": {
			buildErr:   realErr,
			monitorErr: canceled,
			wantSubstr: dupMsg,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := chooseSolveError(tc.buildErr, tc.monitorErr)
			if tc.wantNil {
				require.NoError(t, got)
				return
			}

			require.Error(t, got)
			require.Contains(t, got.Error(), tc.wantSubstr)
		})
	}
}
