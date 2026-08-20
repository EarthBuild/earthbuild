//go:build linux && integration

package cli_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/internal/corpus"
)

// The options an invocation carries are read in every spelling the tree uses.
//
// `passable` decides what the gate attempts, so a spelling it cannot read is
// coverage the gate silently declines - which is why the ones it genuinely
// cannot pass are named rather than dropped, and why the ones it can are tested
// here (E463).
func TestTheOptionsAnInvocationCarries(t *testing.T) {
	t.Setenv("A_SECRET_THE_ENV_HAS", "shhh")

	for _, tc := range []struct {
		name    string
		extra   []string
		args    map[string]string
		secrets map[string]string
		noCache bool
		argFile string
		why     string
	}{{
		name:  "a build argument, separated",
		extra: []string{"--build-arg", "X=1"},
		args:  map[string]string{"X": "1"},
	}, {
		name:  "a build argument, joined",
		extra: []string{"--build-arg=X=1"},
		args:  map[string]string{"X": "1"},
	}, {
		name:    "a secret with its value",
		extra:   []string{"--secret", "S=v"},
		secrets: map[string]string{"S": "v"},
	}, {
		name:    "a secret, joined",
		extra:   []string{"--secret=S=v"},
		secrets: map[string]string{"S": "v"},
	}, {
		// The tree's own spelling: `ENV SECRET1=foo` and then the name alone.
		name:    "a secret the environment supplies",
		extra:   []string{"--secret", "A_SECRET_THE_ENV_HAS"},
		secrets: map[string]string{"A_SECRET_THE_ENV_HAS": "shhh"},
	}, {
		name:  "a secret nothing supplies",
		extra: []string{"--secret", "A_SECRET_NOBODY_SET"},
		why:   "--secret A_SECRET_NOBODY_SET, which this environment does not have",
	}, {
		name:    "an instruction about the cache",
		extra:   []string{"--no-cache"},
		noCache: true,
	}, {
		// The project's own files, which the engine reads now (E465).
		name:    "a build argument file",
		extra:   []string{"--arg-file-path", ".some-other-arg"},
		argFile: ".some-other-arg",
	}, {
		// A feature this engine always provides, so the invocation is attempted
		// and the targets are refused as the tree says they must be (E464).
		name:  "an override this engine already satisfies",
		extra: []string{"--version-flag-overrides=require-force-for-unsafe-saves"},
	}, {
		// And any other override is not claimed.
		name:  "an override this engine has not checked",
		extra: []string{"--version-flag-overrides=something-else"},
		why:   "--version-flag-overrides=something-else",
	}, {
		name:  "an option this gate cannot pass",
		extra: []string{"--push"},
		why:   "--push",
	}} {
		got, why := passable(corpus.Invocation{Extra: tc.extra})

		if why != tc.why {
			t.Errorf("%s: reason %q, want %q", tc.name, why, tc.why)

			continue
		}

		if why != "" {
			continue
		}

		if got.ArgFile != tc.argFile {
			t.Errorf("%s: argument file %q, want %q", tc.name, got.ArgFile, tc.argFile)
		}

		if got.NoCache != tc.noCache {
			t.Errorf("%s: no-cache %v, want %v", tc.name, got.NoCache, tc.noCache)
		}

		for k, v := range tc.args {
			if got.Args[k] != v {
				t.Errorf("%s: argument %s is %q, want %q", tc.name, k, got.Args[k], v)
			}
		}

		for k, v := range tc.secrets {
			if got.Secrets[k] != v {
				t.Errorf("%s: secret %s is %q, want %q", tc.name, k, got.Secrets[k], v)
			}
		}
	}
}
