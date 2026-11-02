package features

import (
	"testing"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/stretchr/testify/require"
)

func TestCopyLinkVersionSelection(t *testing.T) {
	t.Parallel()

	const (
		modernVersion = "0.8"
		linkFlag      = "--use-copy-link"
		noLinkFlag    = "--no-use-copy-link"
	)

	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"legacy default", []string{"0.6"}, false},
		{"modern default", []string{modernVersion}, true},
		{"modern opt out", []string{noLinkFlag, modernVersion}, false},
		{"first linked version opt out", []string{noLinkFlag, "0.7"}, false},
		{"legacy opt in", []string{linkFlag, "0.6"}, true},
		{"explicit opt out wins", []string{linkFlag, noLinkFlag, modernVersion}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f, _, err := Get(&earthfile.Version{Args: tc.args})
			require.NoError(t, err)

			_, err = f.ProcessFlags()
			require.NoError(t, err)
			require.Equal(t, tc.want, f.UseCopyLink)

			if f.NoUseCopyLink {
				require.Contains(t, f.String(), noLinkFlag, "VERSION string lost explicit opt out")
			}
		})
	}
}

func TestCopyLinkOverrideDisablesModernDefault(t *testing.T) {
	t.Parallel()

	f := &Features{Major: 0, Minor: 8}
	_, err := f.ProcessFlags()
	require.NoError(t, err)
	require.True(t, f.UseCopyLink)

	err = ApplyFlagOverrides(f, "no-use-copy-link")
	require.NoError(t, err)
	require.False(t, f.UseCopyLink)
}
