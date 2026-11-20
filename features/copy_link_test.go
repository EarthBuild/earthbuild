package features

import (
	"testing"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/stretchr/testify/require"
)

// TestCopyLinkVersionSelection checks that explicit opt-outs win over version
// defaults and positive flags and survive VERSION serialization.
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

// TestCopyLinkRejectsInvalidVersionArgs keeps the opt-out subject to VERSION's
// required version argument and its prohibition on explicit boolean values.
func TestCopyLinkRejectsInvalidVersionArgs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		wantErr string
		args    []string
	}{
		{
			name:    "missing version",
			args:    []string{"--no-use-copy-link"},
			wantErr: errUnexpectedArgs.Error(),
		},
		{
			name:    "explicit false value",
			args:    []string{"--no-use-copy-link=false", "0.8"},
			wantErr: "bool flag `--no-use-copy-link' cannot have an argument",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := Get(&earthfile.Version{Args: tc.args})
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// TestCopyLinkOverrideDisablesModernDefault checks opt-out precedence when an
// override is applied after version defaults have already enabled linking.
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
