package buildcontext

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/domain"
)

func TestEarthfileNotExistErrorMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
		err  EarthfileNotExistError
	}{
		{
			name: "local",
			err:  EarthfileNotExistError{Target: "+deploy", Dir: "/work/proj"},
			want: "no Earthfile or build.earth file found in /work/proj for target +deploy " +
				"(run earth from the directory that contains the Earthfile, " +
				"or reference the target by its path, e.g. ./path/to/dir+target)",
		},
		{
			name: "remote",
			err:  EarthfileNotExistError{Target: "github.com/foo/bar/sub+deploy", Dir: "sub", Remote: true},
			want: "no Earthfile or build.earth file found in repository directory sub " +
				"for target github.com/foo/bar/sub+deploy",
		},
		{
			name: "no target or dir",
			err:  EarthfileNotExistError{},
			want: "no Earthfile or build.earth file found " +
				"(run earth from the directory that contains the Earthfile, " +
				"or reference the target by its path, e.g. ./path/to/dir+target)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.err.Error())
		})
	}
}

func TestDetectBuildFileMissing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	ref, err := domain.ParseTarget("./sub+deploy")
	require.NoError(t, err)

	_, err = detectBuildFile(ref, dir)

	notExist, ok := errors.AsType[EarthfileNotExistError](err)
	require.True(t, ok, "want EarthfileNotExistError, got %v", err)
	require.Equal(t, "./sub+deploy", notExist.Target)
	require.Equal(t, dir, notExist.Dir)
	require.False(t, notExist.Remote)
}

// TestResolveLocalEarthfileNotExist checks that the error surfaced for a
// missing Earthfile names the target being resolved, even when the (per
// directory) build file lookup was cached by a different target, and that it
// is not wrapped with cache-internal context.
//
//nolint:paralleltest // Uses t.Chdir.
func TestResolveLocalEarthfileNotExist(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o700))
	t.Chdir(dir)

	lr := newLocalResolver("", conslogging.Current(0, conslogging.Info, false))

	for _, name := range []string{"./sub+first", "./sub+second"} {
		ref, err := domain.ParseTarget(name)
		require.NoError(t, err)

		_, err = lr.resolveLocal(t.Context(), nil, nil, ref, "")
		require.Error(t, err)

		notExist, ok := errors.AsType[EarthfileNotExistError](err)
		require.True(t, ok, "want EarthfileNotExistError, got %v", err)
		require.Equal(t, name, notExist.Target)

		require.NotContains(t, err.Error(), "cache:")

		wantDir, absErr := filepath.Abs("sub")
		require.NoError(t, absErr)
		require.Equal(t, wantDir, notExist.Dir)
	}
}
