package flag

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/cmd/earth/common"
	"github.com/EarthBuild/earthbuild/util/cliutil"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

// When the binary is built without -X main.DefaultInstallationName (go build,
// go install), RootFlags falls back to the installation name "earth", so the CLI
// uses ~/.earth. GetEarthDir has its own fallback for an empty name (used by
// the autocomplete path, which runs before flags are parsed) and must pick the
// same directory, or autocomplete writes its logs to a different directory from
// the rest of the CLI.
func TestInstallationNameFallbacksAgree(t *testing.T) {
	t.Parallel()

	var (
		global  Global
		rootDef string
	)

	for _, f := range global.RootFlags("", "") {
		sf, ok := f.(*cli.StringFlag)
		if ok && sf.Name == "installation-name" {
			rootDef = sf.Value
		}
	}

	require.NotEmpty(t, rootDef, "installation-name flag not found")

	require.Equal(t, "."+rootDef, filepath.Base(cliutil.GetEarthDir("")),
		"GetEarthDir's fallback must match RootFlags' default installation name")
}

// GetBinaryName's fallback (used when os.Args is empty) must name the
// installed binary, which is now earth.
//
//nolint:paralleltest // mutates the process-wide os.Args
func TestGetBinaryNameFallback(t *testing.T) {
	saved := os.Args
	os.Args = nil

	t.Cleanup(func() { os.Args = saved })

	require.Equal(t, "earth", common.GetBinaryName())
}
