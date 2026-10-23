package common

import (
	"testing"

	"github.com/EarthBuild/earthbuild/util/cliutil"
	"github.com/stretchr/testify/require"
)

func TestBinaryName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
		args []string
	}{
		{
			name: "nil args",
			args: nil,
			want: cliutil.DefaultInstallationName,
		},
		{
			name: "empty args slice",
			args: []string{},
			want: cliutil.DefaultInstallationName,
		},
		{
			name: "empty first arg",
			args: []string{""},
			want: cliutil.DefaultInstallationName,
		},
		{
			name: "dot first arg",
			args: []string{"."},
			want: cliutil.DefaultInstallationName,
		},
		{
			name: "slash first arg",
			args: []string{"/"},
			want: cliutil.DefaultInstallationName,
		},
		{
			name: "bare binary name",
			args: []string{"earth"},
			want: cliutil.DefaultInstallationName,
		},
		{
			name: "full path with subcommand args",
			args: []string{"/usr/local/bin/earth", "build", "+all"},
			want: cliutil.DefaultInstallationName,
		},
		{
			name: "legacy symlink path",
			args: []string{"/usr/local/bin/earthly"},
			want: "earthly",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, binaryName(tt.args))
		})
	}
}
