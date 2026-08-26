package earthfile2llb

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The flags built here are consumed by buildkitd/dockerd-wrapper.sh, which is
// baked into the buildkitd image. Keep the two in sync.
func TestMakeWithDockerdWrapFun(t *testing.T) {
	t.Parallel()

	const dindID = "abc123"

	for _, tc := range []struct {
		tarPaths        []string
		imgsWithDigests []string
		expected        []string
		notExpected     []string
		name            string
		dindID          string
		opt             WithDockerOpt
	}{
		{
			name:     "minimal",
			dindID:   dindID,
			expected: []string{"--data-root='/var/earthbuild/dind/" + dindID + "'"},
			notExpected: []string{
				"--cache-data", startComposeFlag, "--load-file", "--compose-file",
			},
		},
		{
			name:     "cache id implies --cache-data",
			dindID:   "cache_mycache",
			expected: []string{"--data-root='/var/earthbuild/dind/cache_mycache'", "--cache-data"},
		},
		{
			name:            "one flag per tar path and digest",
			dindID:          dindID,
			tarPaths:        []string{"/tmp/a.tar", "/tmp/b.tar"},
			imgsWithDigests: []string{"alpine@sha256:aaa"},
			expected: []string{
				"--load-file='/tmp/a.tar'",
				"--load-file='/tmp/b.tar'",
				"--image-digest='alpine@sha256:aaa'",
			},
		},
		{
			name:   "compose files and services",
			dindID: dindID,
			opt: WithDockerOpt{
				ComposeFiles:    []string{"docker-compose.yml", "override.yml"},
				ComposeServices: []string{"db", "cache"},
			},
			expected: []string{
				startComposeFlag,
				"--compose-file='docker-compose.yml'",
				"--compose-file='override.yml'",
				"--compose-service='db'",
				"--compose-service='cache'",
			},
		},
		{
			name:     "values with single quotes are escaped",
			dindID:   dindID,
			tarPaths: []string{"/tmp/it's.tar"},
			expected: []string{`--load-file='/tmp/it'"'"'s.tar'`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			wrapFun := makeWithDockerdWrapFun(tc.dindID, tc.tarPaths, tc.imgsWithDigests, tc.opt)
			got := wrapFun([]string{"echo", "hello"}, nil, true, false, false)

			require.Len(t, got, 3)
			assert.Equal(t, []string{shellPath, "-c"}, got[:2])

			cmd := got[2]

			// The wrapper's flags have to come before the "--" that separates
			// them from the user's command.
			flags, userCmd, found := strings.Cut(cmd, " -- ")
			require.True(t, found, "expected a -- separator in %q", cmd)
			assert.Contains(t, flags, dockerdWrapperPath+" execute")
			assert.Contains(t, userCmd, "echo hello")

			for _, want := range tc.expected {
				assert.Contains(t, flags, want)
			}

			for _, notWant := range tc.notExpected {
				assert.NotContains(t, flags, notWant)
			}
		})
	}
}

func TestComposeArgsNoComposeFiles(t *testing.T) {
	t.Parallel()

	// Without compose files there is nothing for the wrapper to start, so it
	// must not receive --start-compose. Services alone do not enable compose.
	assert.Empty(t, composeArgs(WithDockerOpt{}))
	assert.NotContains(
		t,
		composeArgs(WithDockerOpt{ComposeServices: []string{"db"}}),
		startComposeFlag,
	)
}
