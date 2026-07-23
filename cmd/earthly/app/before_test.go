package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAutoSkipDeprecationWarning(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		localSkipDB  string
		skipBuildkit bool
		noAutoSkip   bool
		wantWarning  bool
	}{
		{
			name:        "no auto-skip flags set",
			wantWarning: false,
		},
		{
			name:         "--auto-skip set",
			skipBuildkit: true,
			wantWarning:  true,
		},
		{
			name:        "--no-auto-skip set",
			noAutoSkip:  true,
			wantWarning: true,
		},
		{
			name:        "--auto-skip-db-path set",
			localSkipDB: "/tmp/skip.db",
			wantWarning: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			warning := autoSkipDeprecationWarning(tc.skipBuildkit, tc.noAutoSkip, tc.localSkipDB)
			if tc.wantWarning {
				require.Contains(t, warning, "Deprecation:")
				require.Contains(t, warning, "discussions/707")
			} else {
				require.Empty(t, warning)
			}
		})
	}
}
