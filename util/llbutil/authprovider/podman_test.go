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

	"git.sr.ht/~nelsam/correct/match"
	"git.sr.ht/~nelsam/correct/result"
	"git.sr.ht/~nelsam/hel/pkg/pers"
	"github.com/EarthBuild/earthbuild/util/llbutil/authprovider"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth"
	"github.com/stretchr/testify/require"
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

	type testCtx struct {
		os     *mockOS
		stderr *mockWriter
		result chan session.Attachable
	}

	type credentials interface {
		Credentials(ctx context.Context, req *auth.CredentialsRequest) (*auth.CredentialsResponse, error)
	}

	setup := func(t *testing.T) testCtx {
		t.Helper()

		tt := testCtx{
			os:     newMockOS(pers.WithTimeout(t, mockTimeout)),
			stderr: newMockWriter(pers.WithTimeout(t, mockTimeout)),
			result: make(chan session.Attachable),
		}

		go func() {
			defer close(tt.result)

			tt.result <- authprovider.NewPodman(tt.stderr, authprovider.WithOS(tt.os))
		}()

		t.Cleanup(func() {
			_, ok := <-tt.result
			require.False(t, ok) // Ensure that the channel was closed
		})

		return tt
	}

	type authFile struct {
		path   any // can be a string or a matcher
		host   string
		user   string
		secret string
	}

	type entry struct {
		auth *authFile
		envs []string
	}

	matchRegexp := func(pattern string) match.Match[string] {
		return func(s string) match.Result {
			matched, err := regexp.MatchString(pattern, s)
			if err != nil {
				return result.Simplef(false, s, "regexp error: %v", err)
			}

			return result.Simplef(matched, s, "matches regexp %q", pattern)
		}
	}

	for _, tt := range []struct {
		name  string
		entry entry
	}{
		{
			name: "it prefers REGISTRY_AUTH_FILE",
			entry: entry{
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
		},
		{
			name: "it falls back to XDG_RUNTIME_DIR/containers/auth.json",
			entry: entry{
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
		},
		{
			name: "it checks the root runtime dir last",
			entry: entry{
				envs: []string{
					"REGISTRY_AUTH_FILE=",
					"XDG_RUNTIME_DIR=",
				},
				auth: &authFile{
					path:   matchRegexp("/run/containers/[0-9]*/auth.json"),
					host:   "foo",
					user:   "bar",
					secret: "baz",
				},
			},
		},
		{
			name: "it returns a provider even when no podman auth file exists",
			entry: entry{
				envs: []string{
					"REGISTRY_AUTH_FILE=",
					"XDG_RUNTIME_DIR=",
				},
				auth: nil,
			},
		},
	} {
		e := tt.entry
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tc := setup(t)

			for _, env := range e.envs {
				name, val, ok := strings.Cut(env, "=")
				require.True(t, ok)
				pers.MethodWasCalled(t, tc.os.method.Getenv,
					pers.Within(timeout),
					pers.WithArgs(name),
					pers.Returning(val),
				)
			}

			if e.auth == nil {
				pers.MethodWasCalled(t,
					tc.os.method.Open, pers.Within(timeout), pers.WithArgs(pers.Any), pers.Returning(nil, fs.ErrNotExist))

				select {
				case res := <-tc.result:
					_, ok := res.(credentials)
					require.True(t, ok)
				case <-time.After(timeout):
					t.Fatalf("timed out waiting to fall back to the default docker auth provider")
				}

				return
			}

			creds := base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "%s:%s", e.auth.user, e.auth.secret))
			authFile := io.NopCloser(bytes.NewBufferString(fmt.Sprintf(authFmt, e.auth.host, creds)))
			pers.MethodWasCalled(t, tc.os.method.Open,
				pers.Within(timeout),
				pers.WithArgs(e.auth.path),
				pers.Returning(authFile, nil),
			)

			select {
			case res := <-tc.result:
				credsIntf, ok := res.(credentials)
				require.True(t, ok)

				req := &auth.CredentialsRequest{
					Host: e.auth.host,
				}

				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()

				resp, err := credsIntf.Credentials(ctx, req)
				require.NoError(t, err)
				require.Equal(t, e.auth.user, resp.GetUsername())
				require.Equal(t, e.auth.secret, resp.GetSecret())
			case <-time.After(timeout):
				t.Fatalf("timed out waiting for a podman auth provider")
			}
		})
	}
}
