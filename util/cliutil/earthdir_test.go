package cliutil

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetEarthDir(t *testing.T) {
	t.Parallel()

	require.Equal(t, ".earth", filepath.Base(GetEarthDir("")))

	tests := []struct {
		name        string
		installName string
		wantBase    string
	}{
		{
			name:        "empty install name falls back to default",
			installName: "",
			wantBase:    ".earth",
		},
		{
			name:        "custom install name after empty call",
			installName: "custom",
			wantBase:    ".custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.wantBase, filepath.Base(GetEarthDir(tt.installName)))

			dir, _ := getEarthDirAndUser(tt.installName)
			require.Equal(t, tt.wantBase, filepath.Base(dir))
		})
	}
}

//nolint:paralleltest,tparallel // Subtests run sequentially to verify sequential installation name transitions.
func TestGetOrCreateEarthDir_MultiInstallSequencing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		installName string
		wantBase    string
	}{
		{
			name:        "fallback default installation",
			installName: "",
			wantBase:    ".earth",
		},
		{
			name:        "first custom installation",
			installName: "earth-test-seq-1",
			wantBase:    ".earth-test-seq-1",
		},
		{
			name:        "second custom installation",
			installName: "earth-test-seq-2",
			wantBase:    ".earth-test-seq-2",
		},
		{
			name:        "repeated first custom installation",
			installName: "earth-test-seq-1",
			wantBase:    ".earth-test-seq-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, err := GetOrCreateEarthDir(tt.installName)
			require.NoError(t, err)
			require.Equal(t, tt.wantBase, filepath.Base(dir))

			if tt.installName != "" {
				t.Cleanup(func() { _ = os.RemoveAll(dir) })
			}

			fi, err := os.Stat(dir)
			require.NoError(t, err)
			require.True(t, fi.IsDir())
		})
	}
}

func TestGetOrCreateEarthDir_TransientFailureDoesNotLatch(t *testing.T) {
	t.Parallel()

	home, _ := getEarthDirAndUser("probe")
	baseHome := filepath.Dir(home)

	collisionName := "earth-test-transient-unlatch"
	collidingPath := filepath.Join(baseHome, "."+collisionName)

	// Create a regular file blocking directory creation
	f, err := os.Create(collidingPath) // #nosec G304
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// Call fails due to file collision
	_, err = GetOrCreateEarthDir(collisionName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "create earth dir")

	// Remove blocking file
	require.NoError(t, os.Remove(collidingPath))
	t.Cleanup(func() { _ = os.RemoveAll(collidingPath) })

	// Subsequent call must succeed and not be permanently latched
	dir, err := GetOrCreateEarthDir(collisionName)
	require.NoError(t, err)
	require.Equal(t, collidingPath, dir)

	fi, err := os.Stat(dir)
	require.NoError(t, err)
	require.True(t, fi.IsDir())
}

func TestGetOrCreateEarthDir_ConcurrentSafe(t *testing.T) {
	t.Parallel()

	installName := "earth-test-concurrent-init"

	var (
		wg      sync.WaitGroup
		errs    = make([]error, 16)
		dirs    = make([]string, 16)
		workers = 16
	)

	home, _ := getEarthDirAndUser(installName)

	t.Cleanup(func() { _ = os.RemoveAll(home) })

	for i := range workers {
		wg.Add(1)

		go func(idx int) {
			defer wg.Done()

			dirs[idx], errs[idx] = GetOrCreateEarthDir(installName)
		}(i)
	}

	wg.Wait()

	for i := range workers {
		require.NoError(t, errs[i], "worker %d failed", i)
		require.Equal(t, home, dirs[i])
	}
}

func TestUserNameFormatting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		user *user.User
		want string
	}{
		{
			name: "nil user",
			user: nil,
			want: "<nil>",
		},
		{
			name: "user with username",
			user: &user.User{Username: "gopher", Uid: "1000"},
			want: "gopher",
		},
		{
			name: "user with empty username falls back to uid",
			user: &user.User{Username: "", Uid: "1001"},
			want: "1001",
		},
		{
			name: "user with empty username and uid",
			user: &user.User{Username: "", Uid: ""},
			want: "<unknown>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := userName(tt.user)
			require.Equal(t, tt.want, got)

			msg := fmt.Sprintf("failed to ensure /dir is owned by %s: err", userName(tt.user))
			require.NotContains(t, msg, "&{")
		})
	}
}
