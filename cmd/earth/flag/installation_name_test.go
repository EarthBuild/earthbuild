package flag

import (
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/util/cliutil"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

// When the binary is built without -X main.DefaultInstallationName (go build,
// go install), RootFlags and GetEarthDir must use the same installation name.
// GetEarthDir may select ~/.earthly if it is the only existing directory.
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

	require.Equal(t, cliutil.DefaultInstallationName, rootDef)
	require.Equal(t, filepath.Base(cliutil.GetEarthDir(rootDef)), filepath.Base(cliutil.GetEarthDir("")),
		"GetEarthDir's fallback must match RootFlags' default installation name")
}
