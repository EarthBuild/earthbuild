package image

import (
	"testing"
	"time"

	"github.com/moby/buildkit/exporter/containerimage/image"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	archArm64 = "arm64"
	cmdShell  = "CMD-SHELL"
	osLinux   = "linux"
)

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
		bkImg.Architecture = archArm64
		bkImg.OS = osLinux

		img := FromBuildKit(bkImg)
		require.NotNil(t, img)

		want := &Image{
			Architecture: archArm64,
			OS:           osLinux,
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
