package containerutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_parseDockerVersion(t *testing.T) {
	t.Parallel()

	dockerJSON := `{
		"Client": {
			"Version": "29.8.0",
			"ApiVersion": "1.56",
			"DefaultAPIVersion": "1.56",
			"GitCommit": "88096ef",
			"GoVersion": "go1.26.8",
			"Os": "darwin",
			"Arch": "arm64",
			"BuildTime": "Thu Sep  3 21:49:43 2026",
			"Context": "desktop-linux"
		},
		"Server": {
			"Version": "29.8.0",
			"ApiVersion": "1.56",
			"MinAPIVersion": "1.24",
			"GitCommit": "c04fa2d",
			"GoVersion": "go1.26.8",
			"Os": "linux",
			"Arch": "arm64",
			"KernelVersion": "6.10.14-linuxkit",
			"BuildTime": "Thu Sep  3 21:52:10 2026"
		}
	}`

	info, err := parseDockerVersion(dockerJSON, "/var/run/docker.sock")
	require.NoError(t, err)

	assert.Equal(t, "29.8.0", info.ClientVersion)
	assert.Equal(t, "1.56", info.ClientAPIVersion)
	assert.Equal(t, "darwin/arm64", info.ClientPlatform)
	assert.Equal(t, "29.8.0", info.ServerVersion)
	assert.Equal(t, "1.56", info.ServerAPIVersion)
	assert.Equal(t, "linux/arm64", info.ServerPlatform)
	assert.Equal(t, "/var/run/docker.sock", info.ServerAddress)
}

func Test_parseDockerVersion_nullServer(t *testing.T) {
	t.Parallel()

	dockerJSON := `{
		"Client": {
			"Version": "29.8.0",
			"ApiVersion": "1.56",
			"Os": "darwin",
			"Arch": "arm64"
		},
		"Server": null
	}`

	info, err := parseDockerVersion(dockerJSON, "/var/run/docker.sock")
	require.NoError(t, err)

	assert.Equal(t, "29.8.0", info.ClientVersion)
	assert.Equal(t, "1.56", info.ClientAPIVersion)
	assert.Equal(t, "darwin/arm64", info.ClientPlatform)
	assert.Empty(t, info.ServerVersion)
	assert.Empty(t, info.ServerAPIVersion)
	assert.Equal(t, "/", info.ServerPlatform)
	assert.Equal(t, "/var/run/docker.sock", info.ServerAddress)
}
