package shell

import "github.com/EarthBuild/earthbuild/internal/earthfile"

// IsValidEnvVarName returns true if env name is valid.
func IsValidEnvVarName(name string) bool {
	return earthfile.IsValidEnvVarName(name)
}
