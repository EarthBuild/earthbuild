package base

import (
	"testing"

	"github.com/EarthBuild/earthbuild/internal/engine"
	"github.com/stretchr/testify/assert"
)

func TestLocalRegistryHost(t *testing.T) {
	t.Parallel()

	const configured = "tcp://127.0.0.1:8381"

	// Docker and Podman publish the registry on the configured host port; replacing it with the
	// container port (8371) makes the settings hash drift and buildkitd restart onto 8371.
	for _, tt := range []struct {
		name   string
		want   string
		scheme engine.Scheme
	}{
		{"docker keeps configured host port", configured, engine.SchemeDocker},
		{"podman keeps configured host port", configured, engine.SchemePodman},
		// Apple Container publishes no host ports; the mock stands in its container address.
		{"apple uses container address", "tcp://127.0.0.1:8371", engine.SchemeApple},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			eng := engine.NewTestClient(engine.Metadata{Scheme: tt.scheme})
			assert.Equal(t, tt.want, localRegistryHost(t.Context(), eng, "earth-buildkitd", configured))
		})
	}

	t.Run("empty stays empty", func(t *testing.T) {
		t.Parallel()

		eng := engine.NewTestClient(engine.Metadata{Scheme: engine.SchemeApple})
		assert.Empty(t, localRegistryHost(t.Context(), eng, "earth-buildkitd", ""))
	})
}
