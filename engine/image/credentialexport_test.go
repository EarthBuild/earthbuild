package image

import (
	"testing"

	"github.com/docker/cli/cli/config/configfile"
	"github.com/docker/cli/cli/config/types"
)

// LoginForTest makes this machine look logged in to one registry for the length
// of a test, so a pull can be driven against a registry that refuses anonymous
// callers without anybody having really logged in to anything.
func LoginForTest(t *testing.T, host, user, secret string) {
	t.Helper()

	was := dockerConfig
	dockerConfig = func() *configfile.ConfigFile {
		return &configfile.ConfigFile{
			AuthConfigs: map[string]types.AuthConfig{
				host: {Username: user, Password: secret},
			},
		}
	}

	credentials.Clear()

	t.Cleanup(func() {
		dockerConfig = was
		credentials.Clear()
	})
}

// LogOutForTest is the other half: a machine with nothing stored, whatever the
// developer running the tests happens to have logged in to.
func LogOutForTest(t *testing.T) {
	t.Helper()

	was := dockerConfig
	dockerConfig = func() *configfile.ConfigFile { return &configfile.ConfigFile{} }

	credentials.Clear()

	t.Cleanup(func() {
		dockerConfig = was
		credentials.Clear()
	})
}
