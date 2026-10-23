package image

import (
	"testing"
	"time"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	archArm64  = "arm64"
	cmdShell   = "CMD-SHELL"
	osLinux    = "linux"
	valMutated = "MUTATED"
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

		bkImg := &dockerspec.DockerOCIImage{
			Config: dockerspec.DockerOCIImageConfig{
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
				Healthcheck: &dockerspec.HealthcheckConfig{
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
				Healthcheck: &dockerspec.HealthcheckConfig{
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

func TestImage_Clone_PreservesScalarsAndIsolation(t *testing.T) {
	t.Parallel()

	orig := &Image{
		Architecture: archArm64,
		OS:           osLinux,
		Config: Config{
			ImageConfig: specs.ImageConfig{
				User:         "nobody",
				Env:          []string{"VAR=val"},
				Entrypoint:   []string{"/bin/entry"},
				Cmd:          []string{"arg1", "arg2"},
				WorkingDir:   "/home",
				StopSignal:   "SIGTERM",
				ExposedPorts: map[string]struct{}{"80/tcp": {}},
				Volumes:      map[string]struct{}{"/vol": {}},
				Labels:       map[string]string{"k": "v"},
			},
			Healthcheck: &dockerspec.HealthcheckConfig{
				Test:        []string{"CMD", "check"},
				Interval:    10 * time.Second,
				Timeout:     3 * time.Second,
				StartPeriod: 1 * time.Second,
				Retries:     5,
			},
		},
	}

	clone := orig.Clone()
	require.NotNil(t, clone)
	assert.Equal(t, orig, clone)
	assert.Equal(t, "nobody", clone.Config.User)
	assert.Equal(t, "/home", clone.Config.WorkingDir)
	assert.Equal(t, "SIGTERM", clone.Config.StopSignal)

	// Verify deep copy isolation
	orig.Config.Env[0] = valMutated
	assert.Equal(t, "VAR=val", clone.Config.Env[0])

	orig.Config.Entrypoint[0] = valMutated
	assert.Equal(t, "/bin/entry", clone.Config.Entrypoint[0])

	orig.Config.Cmd[0] = valMutated
	assert.Equal(t, "arg1", clone.Config.Cmd[0])

	orig.Config.Labels["k"] = valMutated
	assert.Equal(t, "v", clone.Config.Labels["k"])

	orig.Config.Healthcheck.Test[0] = valMutated
	assert.Equal(t, "CMD", clone.Config.Healthcheck.Test[0])
}
