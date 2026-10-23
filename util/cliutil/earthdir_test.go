package cliutil

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetEarthDir(t *testing.T) {
	t.Parallel()

	// Call GetEarthDir("") first to ensure caching homeDir does not freeze
	// the installation name for subsequent calls with a custom name.
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
