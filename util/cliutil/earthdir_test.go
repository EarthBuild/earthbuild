package cliutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	defaultInstall = "earth"
	newDirName     = ".earth"
	legacyDirName  = ".earthly"
)

func TestLegacyEarthDirWarning(t *testing.T) {
	// Not parallel: relies on HOME.
	tests := []struct {
		wantErr     error
		name        string
		installName string
		wantDir     string
		dirs        []string
		wantWarning bool
	}{
		{name: "neither exists", installName: defaultInstall, wantDir: newDirName},
		{name: "only new exists", dirs: []string{newDirName}, installName: defaultInstall, wantDir: newDirName},
		{
			name: "only legacy exists", dirs: []string{legacyDirName}, installName: defaultInstall,
			wantDir: legacyDirName, wantWarning: true,
		},
		{
			name: "both exist", dirs: []string{newDirName, legacyDirName}, installName: "",
			wantDir: newDirName, wantErr: ErrBothEarthDirsExist,
		},
		{name: "custom name ignores legacy", dirs: []string{legacyDirName}, installName: "earth-dev", wantDir: ".earth-dev"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("SUDO_USER", "")

			for _, d := range tt.dirs {
				require.NoError(t, os.MkdirAll(filepath.Join(home, d), 0o750))
			}

			warning, err := LegacyEarthDirWarning(tt.installName)
			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.wantWarning, warning != "")

			dir, _ := getEarthDirAndUser(tt.installName)
			require.Equal(t, filepath.Join(home, tt.wantDir), dir)
		})
	}
}
