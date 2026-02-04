package authprovider_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/util/llbutil/authprovider"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth"
	"github.com/stretchr/testify/mock"
)

const (
	authFmt = `
{
  "auths": {
    "%s": {
      "auth": "%s"
    }
  }
}
`
)

func TestPodmanProvider(t *testing.T) {
	t.Parallel()

	type credentials interface {
		Credentials(ctx context.Context, req *auth.CredentialsRequest) (*auth.CredentialsResponse, error)
	}

	type authFile struct {
		path   any // can be a string or a matcher
		host   string
		user   string
		secret string
	}

	type entry struct {
		name string
		auth *authFile
		envs []string
	}

	tests := []entry{
		{
			name: "it prefers REGISTRY_AUTH_FILE",
			envs: []string{
				"REGISTRY_AUTH_FILE=/path/to/someFile",
			},
			auth: &authFile{
				path:   "/path/to/someFile",
				host:   "foo.bar",
				user:   "foo",
				secret: "bar",
			},
		},
		{
			name: "it falls back to XDG_RUNTIME_DIR/containers/auth.json",
			envs: []string{
				"REGISTRY_AUTH_FILE=",
				"XDG_RUNTIME_DIR=/path/to/some/dir",
			},
			auth: &authFile{
				path:   "/path/to/some/dir/containers/auth.json",
				host:   "bacon.eggs",
				user:   "eggs",
				secret: "bacon",
			},
		},
		{
			name: "it checks the root runtime dir last",
			envs: []string{
				"REGISTRY_AUTH_FILE=",
				"XDG_RUNTIME_DIR=",
			},
			auth: &authFile{
				path:   regexp.MustCompile("/run/containers/[0-9]*/auth.json"),
				host:   "foo",
				user:   "bar",
				secret: "baz",
			},
		},
		{
			name: "it returns a provider even when no podman auth file exists",
			envs: []string{
				"REGISTRY_AUTH_FILE=",
				"XDG_RUNTIME_DIR=",
			},
			auth: nil,
		},
	}

	for _, e := range tests {
		t.Run(e.name, func(t *testing.T) {
			t.Parallel()

			mockOS := newMockOS()
			stderr := newMockWriter()
			result := make(chan session.Attachable)

			// Set up mock expectations for Getenv calls
			for _, env := range e.envs {
				name, val, ok := strings.Cut(env, "=")

				if !ok {
					t.Fatalf("invalid env format: %s", env)
				}

				mockOS.On("Getenv", name).Return(val)
			}

			if e.auth == nil {
				// The code should fall back to the default docker auth provider
				mockOS.On("Open", mock.Anything).Return(nil, fs.ErrNotExist)

				go func() {
					defer close(result)

					result <- authprovider.NewPodman(stderr, authprovider.WithOS(mockOS))
				}()

				select {
				case res := <-result:
					_, ok := res.(credentials)

					if !ok {
						t.Error("expected result to implement credentials interface")
					}
				case <-time.After(timeout):
					t.Fatal("timed out waiting to fall back to the default docker auth provider")
				}

				mockOS.AssertExpectations(t)

				return
			}

			creds := base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "%s:%s", e.auth.user, e.auth.secret))
			authFileContent := io.NopCloser(bytes.NewBufferString(fmt.Sprintf(authFmt, e.auth.host, creds)))

			// Handle both string and regex matchers for path
			switch p := e.auth.path.(type) {
			case string:
				mockOS.On("Open", p).Return(authFileContent, nil)
			case *regexp.Regexp:
				// For regex, we use MatchedBy to match the argument
				mockOS.On("Open", mock.MatchedBy(func(path string) bool {
					return p.MatchString(path)
				})).Return(authFileContent, nil)
			default:
				t.Fatalf("unexpected path type: %T", p)
			}

			go func() {
				defer close(result)

				result <- authprovider.NewPodman(stderr, authprovider.WithOS(mockOS))
			}()

			select {
			case res := <-result:
				creds, ok := res.(credentials)

				if !ok {
					t.Fatal("expected result to implement credentials interface")
				}

				req := &auth.CredentialsRequest{
					Host: e.auth.host,
				}

				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()

				resp, err := creds.Credentials(ctx, req)

				if err != nil {
					t.Errorf("expected no error from Credentials, got: %v", err)
				}

				if resp.Username != e.auth.user {
					t.Errorf("expected username to be %q, got %q", e.auth.user, resp.Username)
				}

				if resp.Secret != e.auth.secret {
					t.Errorf("expected secret to be %q, got %q", e.auth.secret, resp.Secret)
				}
			case <-time.After(timeout):
				t.Fatal("timed out waiting for a podman auth provider")
			}

			// Verify channel was closed
			_, ok := <-result

			if ok {
				t.Error("expected result channel to be closed")
			}

			mockOS.AssertExpectations(t)
		})
	}
}
