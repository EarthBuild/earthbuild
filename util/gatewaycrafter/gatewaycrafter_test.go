package gatewaycrafter

import (
	jsonv1 "encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/states/image"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	bkimage "github.com/moby/buildkit/exporter/containerimage/image"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const cmdShell = "CMD-SHELL"

func TestAddPushImageEntry_ExporterImageConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		imageConfig *image.Image
		name        string
		imageName   string
		wantJSON    string
		refID       int
		shouldPush  bool
	}{
		{
			name:       "with healthcheck and durations",
			refID:      0,
			imageName:  "my-image:latest",
			shouldPush: true,
			imageConfig: &image.Image{
				Architecture: "arm64",
				OS:           "linux",
				Config: image.Config{
					ImageConfig: specs.ImageConfig{
						User:       "testuser",
						Env:        []string{"FOO=bar", "PATH=/usr/bin"},
						Entrypoint: []string{"/bin/sh"},
						Cmd:        []string{"-c", "echo hello"},
						WorkingDir: "/app",
						Labels: map[string]string{
							"version": "1.0",
						},
						Volumes: map[string]struct{}{
							"/data": {},
						},
						ExposedPorts: map[string]struct{}{
							"8080/tcp": {},
						},
					},
					Healthcheck: &bkimage.HealthConfig{
						Test:          []string{cmdShell, "curl -f http://localhost/"},
						Interval:      30 * time.Second,
						Timeout:       5 * time.Second,
						StartPeriod:   10 * time.Second,
						StartInterval: 2 * time.Second,
						Retries:       3,
					},
				},
			},
			wantJSON: `{
				"architecture": "arm64",
				"os": "linux",
				"config": {
					"User": "testuser",
					"ExposedPorts": {
						"8080/tcp": {}
					},
					"Env": [
						"FOO=bar",
						"PATH=/usr/bin"
					],
					"Entrypoint": [
						"/bin/sh"
					],
					"Cmd": [
						"-c",
						"echo hello"
					],
					"Volumes": {
						"/data": {}
					},
					"WorkingDir": "/app",
					"Labels": {
						"version": "1.0"
					},
					"Healthcheck": {
						"Test": [
							"CMD-SHELL",
							"curl -f http://localhost/"
						],
						"Interval": 30000000000,
						"Timeout": 5000000000,
						"StartPeriod": 10000000000,
						"StartInterval": 2000000000,
						"Retries": 3
					}
				}
			}`,
		},
		{
			name:      "without healthcheck",
			refID:     1,
			imageName: "alpine:latest",
			imageConfig: &image.Image{
				Architecture: "amd64",
				OS:           "linux",
				Config: image.Config{
					ImageConfig: specs.ImageConfig{
						WorkingDir: "/root",
					},
				},
			},
			wantJSON: `{
				"architecture": "amd64",
				"os": "linux",
				"config": {
					"WorkingDir": "/root"
				}
			}`,
		},
		{
			name:        "nil imageConfig",
			refID:       2,
			imageName:   "empty:latest",
			imageConfig: nil,
			wantJSON:    "null",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gc := NewGatewayCrafter()
			platform := []byte("linux/arm64")

			refPrefix, err := gc.AddPushImageEntry(nil, tc.refID, tc.imageName, tc.shouldPush, false, tc.imageConfig, platform)
			require.NoError(t, err)

			wantPrefix := fmt.Sprintf("ref/image-%d", tc.refID)
			assert.Equal(t, wantPrefix, refPrefix)

			_, metadata := gc.GetRefsAndMetadata()
			require.NotNil(t, metadata)

			configKey := refPrefix + "/" + exptypes.ExporterImageConfigKey
			configBytes, ok := metadata[configKey]
			require.True(t, ok, "metadata should contain key %q", configKey)

			assert.JSONEq(t, tc.wantJSON, string(configBytes))

			v1Bytes, err := jsonv1.Marshal(tc.imageConfig)
			require.NoError(t, err)
			assert.JSONEq(t, string(v1Bytes), string(configBytes))

			assert.Equal(t, []byte(tc.imageName), metadata[refPrefix+"/image.name"])
			assert.Equal(t, platform, metadata[refPrefix+"/platform"])

			if tc.shouldPush {
				assert.Equal(t, []byte("true"), metadata[refPrefix+"/export-image-push"])
			}
		})
	}
}
