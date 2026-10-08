package authprovider_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/util/llbutil/authprovider"
	"github.com/moby/buildkit/session/auth"
	"github.com/stretchr/testify/require"
)

const authFmt = `
{
  "auths": {
    "%s": {
      "auth": "%s"
    }
  }
}
`

type fakeOS struct {
	files map[string]string
	env   map[string]string
}

func (f fakeOS) Getenv(name string) string {
	if f.env == nil {
		return ""
	}

	return f.env[name]
}

func (f fakeOS) Open(name string) (io.ReadCloser, error) {
	if content, ok := f.files[name]; ok {
		return io.NopCloser(strings.NewReader(content)), nil
	}

	for pattern, content := range f.files {
		if matched, _ := regexp.MatchString("^"+pattern+"$", name); matched {
			return io.NopCloser(strings.NewReader(content)), nil
		}
	}

	return nil, fs.ErrNotExist
}

type credentialsProvider interface {
	Credentials(ctx context.Context, req *auth.CredentialsRequest) (*auth.CredentialsResponse, error)
}

//nolint:goconst
func TestPodmanProvider(t *testing.T) {
	t.Parallel()

	type authFile struct {
		pathPattern string
		host        string
		user        string
		secret      string
	}

	tests := []struct {
		auth *authFile
		envs map[string]string
		name string
	}{
		{
			name: "it prefers REGISTRY_AUTH_FILE",
			envs: map[string]string{
				"REGISTRY_AUTH_FILE": "/path/to/someFile",
			},
			auth: &authFile{
				pathPattern: "/path/to/someFile",
				host:        "foo.bar",
				user:        "foo",
				secret:      "bar",
			},
		},
		{
			name: "it falls back to XDG_RUNTIME_DIR/containers/auth.json",
			envs: map[string]string{
				"REGISTRY_AUTH_FILE": "",
				"XDG_RUNTIME_DIR":    "/path/to/some/dir",
			},
			auth: &authFile{
				pathPattern: "/path/to/some/dir/containers/auth.json",
				host:        "bacon.eggs",
				user:        "eggs",
				secret:      "bacon",
			},
		},
		{
			name: "it checks the root runtime dir last",
			envs: map[string]string{
				"REGISTRY_AUTH_FILE": "",
				"XDG_RUNTIME_DIR":    "",
			},
			auth: &authFile{
				pathPattern: "/run/containers/[0-9]*/auth.json",
				host:        "foo",
				user:        "bar",
				secret:      "baz",
			},
		},
		{
			name: "it returns a provider even when no podman auth file exists",
			envs: map[string]string{
				"REGISTRY_AUTH_FILE": "",
				"XDG_RUNTIME_DIR":    "",
			},
			auth: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fos := fakeOS{
				env:   tt.envs,
				files: make(map[string]string),
			}

			if tt.auth != nil {
				creds := base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "%s:%s", tt.auth.user, tt.auth.secret))
				content := fmt.Sprintf(authFmt, tt.auth.host, creds)
				fos.files[tt.auth.pathPattern] = content
			}

			attachable := authprovider.NewPodman(t.Context(), io.Discard, authprovider.WithOS(fos))
			require.NotNil(t, attachable)

			credsProvider, ok := attachable.(credentialsProvider)
			require.True(t, ok)

			if tt.auth != nil {
				resp, err := credsProvider.Credentials(t.Context(), &auth.CredentialsRequest{
					Host: tt.auth.host,
				})
				require.NoError(t, err)
				require.Equal(t, tt.auth.user, resp.Username)
				require.Equal(t, tt.auth.secret, resp.Secret)
			}
		})
	}
}
