package common

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// buildEarthBinary builds the earth CLI from this module into a temporary
// directory and returns its path, skipping the test if no Go toolchain is
// available.
func buildEarthBinary(t *testing.T) string {
	t.Helper()

	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain unavailable:", err)
	}

	binPath := filepath.Join(t.TempDir(), "earth")

	cmd := exec.Command(goTool, "build", "-o", binPath, "github.com/EarthBuild/earthbuild/cmd/earth")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skip("unable to build earth binary:", err, string(out))
	}

	return binPath
}

func TestIsEarthBinary(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()

	// A file carrying the magic strings the old heuristic grepped for. It is
	// not a Go binary, so it must not be mistaken for one.
	fakePath := filepath.Join(tmp, "fake-earth")
	require.NoError(t, os.WriteFile(fakePath,
		[]byte("docs.earthly.dev api.earthly.dev Earthfile"), 0o600))

	emptyPath := filepath.Join(tmp, "empty")
	require.NoError(t, os.WriteFile(emptyPath, nil, 0o600))

	tests := []struct {
		name     string
		path     func(t *testing.T) string
		expected bool
	}{
		{
			name:     "real binary built from this module",
			path:     buildEarthBinary,
			expected: true,
		},
		{
			name:     "non-Go file containing the legacy magic strings",
			path:     func(*testing.T) string { return fakePath },
			expected: false,
		},
		{
			name:     "nonexistent path",
			path:     func(*testing.T) string { return filepath.Join(tmp, "does-not-exist") },
			expected: false,
		},
		{
			name:     "empty file",
			path:     func(*testing.T) string { return emptyPath },
			expected: false,
		},
		{
			name:     "directory",
			path:     func(*testing.T) string { return tmp },
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, IsEarthBinary(tt.path(t)))
		})
	}
}
