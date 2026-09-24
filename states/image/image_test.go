package image

import (
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/moby/buildkit/exporter/containerimage/image"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const cmdShell = "CMD-SHELL"

func TestConfigMarshalUnmarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		cfg  *Config
		name string
		want string
	}{
		{
			name: "with healthcheck",
			cfg: &Config{
				Healthcheck: &image.HealthConfig{
					Test:     []string{cmdShell, "exit 0"},
					Interval: 15 * time.Second,
					Timeout:  5 * time.Second,
					Retries:  2,
				},
			},
			want: `{
				"Healthcheck": {
					"Test": ["CMD-SHELL", "exit 0"],
					"Interval": 15000000000,
					"Timeout": 5000000000,
					"StartPeriod": 0,
					"StartInterval": 0,
					"Retries": 2
				},
				"ArgsEscaped": false
			}`,
		},
		{
			name: "healthcheck with all durations",
			cfg: &Config{
				Healthcheck: &image.HealthConfig{
					Test:          []string{"CMD", "curl", "-f", "http://localhost"},
					Interval:      30 * time.Second,
					Timeout:       10 * time.Second,
					StartPeriod:   20 * time.Second,
					StartInterval: 2 * time.Second,
					Retries:       3,
				},
			},
			want: `{
				"Healthcheck": {
					"Test": ["CMD", "curl", "-f", "http://localhost"],
					"Interval": 30000000000,
					"Timeout": 10000000000,
					"StartPeriod": 20000000000,
					"StartInterval": 2000000000,
					"Retries": 3
				},
				"ArgsEscaped": false
			}`,
		},
		{
			cfg:  &Config{},
			name: "without healthcheck",
			want: `{"ArgsEscaped": false}`,
		},
		{
			cfg:  nil,
			name: "nil config",
			want: "null",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tc.cfg)
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(data))

			var decoded *Config

			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)
			assert.Equal(t, tc.cfg, decoded)
		})
	}
}

func TestFromBuildKit(t *testing.T) {
	t.Parallel()

	t.Run("nil image", func(t *testing.T) {
		t.Parallel()

		img := FromBuildKit(nil)
		assert.Nil(t, img)
	})

	t.Run("with healthcheck and config", func(t *testing.T) {
		t.Parallel()

		bkImg := &image.Image{
			Config: image.ImageConfig{
				ImageConfig: specs.ImageConfig{
					User:       "testuser",
					Env:        []string{"FOO=bar"},
					Entrypoint: []string{"/bin/sh"},
					Cmd:        []string{"-c", "echo hi"},
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
				Healthcheck: &image.HealthConfig{
					Test:          []string{cmdShell, "curl -f http://localhost/"},
					Interval:      30 * time.Second,
					Timeout:       5 * time.Second,
					StartPeriod:   10 * time.Second,
					StartInterval: 2 * time.Second,
					Retries:       3,
				},
			},
		}
		bkImg.Architecture = "arm64"
		bkImg.OS = "linux"

		img := FromBuildKit(bkImg)
		require.NotNil(t, img)

		want := &Image{
			Architecture: "arm64",
			OS:           "linux",
			Config: Config{
				ImageConfig: specs.ImageConfig{
					User:         "testuser",
					Env:          []string{"FOO=bar"},
					Entrypoint:   []string{"/bin/sh"},
					Cmd:          []string{"-c", "echo hi"},
					WorkingDir:   "/app",
					Labels:       map[string]string{"version": "1.0"},
					Volumes:      map[string]struct{}{"/data": {}},
					ExposedPorts: map[string]struct{}{"8080/tcp": {}},
				},
				Healthcheck: &image.HealthConfig{
					Test:          []string{cmdShell, "curl -f http://localhost/"},
					Interval:      30 * time.Second,
					Timeout:       5 * time.Second,
					StartPeriod:   10 * time.Second,
					StartInterval: 2 * time.Second,
					Retries:       3,
				},
			},
		}

		assert.Equal(t, want, img)

		// Verify deep copy isolation
		bkImg.Config.Env[0] = "MODIFIED"
		assert.Equal(t, "FOO=bar", img.Config.Env[0])

		bkImg.Config.Labels["version"] = "2.0"
		assert.Equal(t, "1.0", img.Config.Labels["version"])

		bkImg.Config.Healthcheck.Interval = 99 * time.Second
		assert.Equal(t, 30*time.Second, img.Config.Healthcheck.Interval)
	})
}
