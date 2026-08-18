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
		wantArticle  string
	}{
		{
			name:        "docker",
			engineName:  "Docker",
			wantName:    "Docker",
			wantDesc:    "Docker container",
			wantArticle: "a Docker container",
		},
		{
			name:        "podman",
			engineName:  "Podman",
			wantName:    "Podman",
			wantDesc:    "Podman container",
			wantArticle: "a Podman container",
		},
		{
			name:        "apple container",
			engineName:  appleContainerName,
			wantName:    appleContainerName,
			wantDesc:    appleContainerName,
			wantArticle: "an Apple Container",
		},
		{
			name:         "fallback to binary",
			engineBinary: "nerdctl",
			wantName:     "nerdctl",
			wantDesc:     "nerdctl container",
			wantArticle:  "a nerdctl container",
		},
		{
			name:         "fallback binary starting with vowel",
			engineBinary: "oci-runtime",
			wantName:     "oci-runtime",
			wantDesc:     "oci-runtime container",
			wantArticle:  "an oci-runtime container",
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
			assert.Equal(t, tt.wantArticle, engineContainerWithArticle(eng))
		})
	}
}
