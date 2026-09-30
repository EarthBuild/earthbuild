package containerutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	daemonlessHost = "daemonless"
	testClientVer  = "6.1.2"
)

func Test_parsePodmanVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want    *FrontendInfo
		name    string
		rawJSON string
		host    string
	}{
		{
			name: "client and server",
			rawJSON: `{
				"Client": {
					"APIVersion": "6.1.2",
					"Version": "6.1.2",
					"GoVersion": "go1.27.1",
					"GitCommit": "",
					"BuiltTime": "Tue Sep 15 19:24:40 2026",
					"Built": 1789496680,
					"BuildOrigin": "brew",
					"OsArch": "darwin/arm64",
					"Os": "darwin"
				},
				"Server": {
					"APIVersion": "6.0.1",
					"Version": "6.0.1",
					"GoVersion": "go1.26.4-X:nodwarf5",
					"GitCommit": "4cabbe61fa3a27fafc4a3ee1226e38ae1664ae57",
					"BuiltTime": "Wed Jul  8 01:00:00 2026",
					"Built": 1783468800,
					"OsArch": "linux/arm64",
					"Os": "linux"
				}
			}`,
			host: daemonlessHost,
			want: &FrontendInfo{
				ClientVersion:    testClientVer,
				ClientAPIVersion: testClientVer,
				ClientPlatform:   "darwin/arm64",
				ServerVersion:    "6.0.1",
				ServerAPIVersion: "6.0.1",
				ServerPlatform:   "linux/arm64",
				ServerAddress:    daemonlessHost,
			},
		},
		{
			name: "null server",
			rawJSON: `{
				"Client": {
					"APIVersion": "6.1.2",
					"Version": "6.1.2",
					"OsArch": "darwin/arm64",
					"Os": "darwin"
				},
				"Server": null
			}`,
			host: daemonlessHost,
			want: &FrontendInfo{
				ClientVersion:    testClientVer,
				ClientAPIVersion: testClientVer,
				ClientPlatform:   "darwin/arm64",
				ServerVersion:    "",
				ServerAPIVersion: "",
				ServerPlatform:   "",
				ServerAddress:    daemonlessHost,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			info, err := parsePodmanVersion(tc.rawJSON, tc.host)
			require.NoError(t, err)
			assert.Equal(t, tc.want, info)
		})
	}
}
