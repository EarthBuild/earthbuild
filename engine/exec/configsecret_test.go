package exec

import (
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// TestASecretInTheImageConfigIsFound.
//
// **A layer is not the only place a credential lands.** The delta scan catches
// a step that wrote a secret into a file; it never looks at the image's own
// configuration, and `ENV TOKEN=$SOME_SECRET` puts the value there - in the
// config blob, which `SAVE IMAGE` persists, which a registry serves to anybody
// who can pull the image, and which `docker inspect` prints without asking.
//
// Worse than a file, in one respect: a file at least has to be read.
//
// Checked on the host, where the values already are, so nothing new crosses the
// wire.
func TestASecretInTheImageConfigIsFound(t *testing.T) {
	t.Parallel()

	secrets := map[string]string{
		"NPM_TOKEN": "npm_aaaaaaaaaaaaaaaaaaaa",
		"UNUSED":    "never-appears-anywhere",
	}

	for _, c := range []struct {
		name string
		cfg  ocispec.ImageConfig
		want string
	}{
		{
			name: "an environment variable",
			cfg:  ocispec.ImageConfig{Env: []string{"PATH=/bin", "TOKEN=npm_aaaaaaaaaaaaaaaaaaaa"}},
			want: "NPM_TOKEN",
		},
		{
			name: "a label",
			cfg:  ocispec.ImageConfig{Labels: map[string]string{"build.token": "npm_aaaaaaaaaaaaaaaaaaaa"}},
			want: "NPM_TOKEN",
		},
		{
			name: "an entrypoint argument",
			cfg:  ocispec.ImageConfig{Entrypoint: []string{"/app", "--token", "npm_aaaaaaaaaaaaaaaaaaaa"}},
			want: "NPM_TOKEN",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := configSecrets(c.cfg, secrets)
			if len(got) != 1 {
				t.Fatalf("found %v, want one mention of %s", got, c.want)
			}

			if !strings.Contains(got[0], c.want) {
				t.Errorf("the finding is %q and does not name %s", got[0], c.want)
			}

			// The value must not travel with the finding - a config leak is
			// reported into the same log the credential was being kept out of.
			if strings.Contains(got[0], "npm_aaaa") {
				t.Errorf("the finding quotes the credential: %s", got[0])
			}
		})
	}

	t.Run("a config with nothing in it", func(t *testing.T) {
		t.Parallel()

		clean := ocispec.ImageConfig{
			Env:    []string{"PATH=/bin", "HOME=/root"},
			Cmd:    []string{"/app", "--serve"},
			Labels: map[string]string{"org.opencontainers.image.title": "app"},
		}

		if got := configSecrets(clean, secrets); len(got) != 0 {
			t.Errorf("found %v in a clean config", got)
		}
	})
}
