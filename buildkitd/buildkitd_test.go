package buildkitd

import (
	"testing"

	"github.com/EarthBuild/earthbuild/internal/engine"
	"github.com/stretchr/testify/assert"
)

const appleContainerName = "Apple Container"

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
			engineName: "Docker",
			wantName:   "Docker",
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
			BuildkitAddress:      "tcp://127.0.0.1:8372",
			LocalRegistryAddress: "http://127.0.0.1:8371",
		}
		updateContainerEndpoints(ctx, "test-container", nil, &settings)
		assert.Equal(t, "tcp://127.0.0.1:8372", settings.BuildkitAddress)
		assert.Equal(t, "http://127.0.0.1:8371", settings.LocalRegistryAddress)
	})

	t.Run("non-apple engine does not overwrite addresses", func(t *testing.T) {
		t.Parallel()

		eng := engine.NewTestClient(engine.Metadata{
			Name:   "Docker",
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
