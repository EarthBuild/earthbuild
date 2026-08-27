package buildkitd

import (
	"testing"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/internal/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	appleContainerName = "Apple Container"
	dockerEngineName   = "Docker"
	defaultBuildkitTCP = "tcp://127.0.0.1:8372"
)

func TestEngineContainer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		engineName   string
		engineBinary string
		wantName     string
		wantDesc     string
	}{
		{
			name:       "docker",
			engineName: dockerEngineName,
			wantName:   dockerEngineName,
			wantDesc:   "Docker container",
		},
		{
			name:       "podman",
			engineName: "Podman",
			wantName:   "Podman",
			wantDesc:   "Podman container",
		},
		{
			name:       "apple container",
			engineName: appleContainerName,
			wantName:   appleContainerName,
			wantDesc:   appleContainerName,
		},
		{
			name:         "fallback to binary",
			engineBinary: "nerdctl",
			wantName:     "nerdctl",
			wantDesc:     "nerdctl container",
		},
		{
			name:         "fallback binary starting with vowel",
			engineBinary: "oci-runtime",
			wantName:     "oci-runtime",
			wantDesc:     "oci-runtime container",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			eng := engine.NewTestClient(engine.Metadata{
				Name:   tt.engineName,
				Binary: tt.engineBinary,
			})

			assert.Equal(t, tt.wantName, engineName(eng))
			assert.Equal(t, tt.wantDesc, engineContainer(eng))
		})
	}
}

func TestContainerName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		installationName string
		want             string
	}{
		{
			installationName: "earth",
			want:             "earth-buildkitd",
		},
		{
			installationName: "earthbuild",
			want:             "earthbuild-buildkitd",
		},
		{
			installationName: "custom",
			want:             "custom-buildkitd",
		},
	}

	for _, tt := range tests {
		t.Run(tt.installationName, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ContainerName(tt.installationName))
		})
	}
}

func TestVolumeName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		installationName string
		want             string
	}{
		{
			installationName: "earth",
			want:             "earth-cache",
		},
		{
			installationName: "earthbuild",
			want:             "earthbuild-cache",
		},
		{
			installationName: "custom",
			want:             "custom-cache",
		},
	}

	for _, tt := range tests {
		t.Run(tt.installationName, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, VolumeName(tt.installationName))
		})
	}
}

func TestUpdateContainerEndpoints(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	t.Run("nil engine does nothing", func(t *testing.T) {
		t.Parallel()

		settings := Settings{
			BuildkitAddress:      defaultBuildkitTCP,
			LocalRegistryAddress: "http://127.0.0.1:8371",
		}
		updateContainerEndpoints(ctx, "test-container", nil, &settings)
		assert.Equal(t, defaultBuildkitTCP, settings.BuildkitAddress)
		assert.Equal(t, "http://127.0.0.1:8371", settings.LocalRegistryAddress)
	})

	t.Run("non-apple engine does not overwrite addresses", func(t *testing.T) {
		t.Parallel()

		eng := engine.NewTestClient(engine.Metadata{
			Name:   dockerEngineName,
			Scheme: engine.SchemeDocker,
		})
		settings := Settings{
			BuildkitAddress:      "docker-container://test-container",
			LocalRegistryAddress: "http://127.0.0.1:8371",
		}
		updateContainerEndpoints(ctx, "test-container", eng, &settings)
		assert.Equal(t, "docker-container://test-container", settings.BuildkitAddress)
		assert.Equal(t, "http://127.0.0.1:8371", settings.LocalRegistryAddress)
	})
}

func TestStart_InvalidAddresses(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	log := conslogging.Current(conslogging.DefaultPadding, conslogging.Info, false)
	eng := engine.NewTestClient(engine.Metadata{
		Name:   dockerEngineName,
		Scheme: engine.SchemeDocker,
	})

	tests := []struct {
		name        string
		errContains string
		settings    Settings
	}{
		{
			name: "invalid buildkit port",
			settings: Settings{
				BuildkitAddress: "tcp://localhost",
				UseTCP:          true,
			},
			errContains: "invalid port in buildkit address",
		},
		{
			name: "invalid local registry port",
			settings: Settings{
				BuildkitAddress:      defaultBuildkitTCP,
				LocalRegistryAddress: "tcp://localhost",
				UseTCP:               true,
			},
			errContains: "invalid port in local registry address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := Start(ctx, log, "test-image", "test-container", eng, tt.settings, false)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestStart_ContainerLocalRegistryAddress(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	log := conslogging.Current(conslogging.DefaultPadding, conslogging.Info, false)
	eng := engine.NewTestClient(engine.Metadata{
		Name:   dockerEngineName,
		Scheme: engine.SchemeDocker,
	})

	settings := Settings{
		BuildkitAddress:      defaultBuildkitTCP,
		LocalRegistryAddress: "docker-container://my-reg",
		UseTCP:               true,
	}

	// Should not fail with port parsing error for docker-container:// scheme.
	err := Start(ctx, log, "test-image", "test-container", eng, settings, false)
	if err != nil {
		assert.NotContains(t, err.Error(), "invalid port in local registry address")
	}
}
