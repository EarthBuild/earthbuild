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

// Regression test for https://github.com/EarthBuild/earthbuild/issues/512.
func TestStripImageDigest(t *testing.T) {
	t.Parallel()

	const (
		sha256Digest = "@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc"
		alpine       = "alpine"
		alpineTagged = "alpine:3.20"
		privateRepo  = "registry.example.com:5000/team/app:v1.2.3"
	)

	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{name: "tag only", in: alpineTagged, want: alpineTagged},
		{name: "no tag", in: alpine, want: alpine},
		{name: "tag and digest", in: alpineTagged + sha256Digest, want: alpineTagged},
		{name: "digest only", in: alpine + sha256Digest, want: alpine},
		{name: "registry with port", in: privateRepo, want: privateRepo},
		{name: "registry with port and digest", in: privateRepo + sha256Digest, want: privateRepo},
		{name: "non-sha256 algorithm", in: alpineTagged + "@sha512:" + strings.Repeat("a", 128), want: alpineTagged},
		{name: "empty", in: "", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, stripImageDigest(tc.in))
		})
	}
}

func TestDropCollidingDigestPulls(t *testing.T) {
	t.Parallel()

	const (
		pinnedTagged = "alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6"
		pinnedOnly   = "alpine@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6"
		pinnedOther  = "alpine@sha256:0000000000000000000000000000000000000000000000000000000000000000"
	)

	pulls := func(names ...string) []DockerPullOpt {
		opts := make([]DockerPullOpt, 0, len(names))
		for _, name := range names {
			opts = append(opts, DockerPullOpt{ImageName: name})
		}

		return opts
	}

	for _, tc := range []struct {
		name        string
		pulls       []DockerPullOpt
		loadNames   []string
		wantKept    []DockerPullOpt
		wantDropped []DockerPullOpt
	}{
		{
			name:     "no digests",
			pulls:    pulls("alpine", "alpine:3.24.2"),
			wantKept: pulls("alpine", "alpine:3.24.2"),
		},
		{
			name:     "pinned images on distinct tags",
			pulls:    pulls(pinnedTagged, pinnedOnly),
			wantKept: pulls(pinnedTagged, pinnedOnly),
		},
		{
			name:        "pinned and unpinned on the implicit latest tag",
			pulls:       pulls(pinnedOnly, "docker.io/library/alpine:latest"),
			wantKept:    pulls("docker.io/library/alpine:latest"),
			wantDropped: pulls(pinnedOnly),
		},
		{
			name:        "pinned and unpinned on the same explicit tag",
			pulls:       pulls("alpine:3.24.2", pinnedTagged),
			wantKept:    pulls("alpine:3.24.2"),
			wantDropped: pulls(pinnedTagged),
		},
		{
			name:        "pinned pull collides with a loaded image",
			pulls:       pulls(pinnedTagged),
			loadNames:   []string{"alpine:3.24.2"},
			wantDropped: pulls(pinnedTagged),
		},
		{
			name:        "two different digests on the same tag",
			pulls:       pulls(pinnedOnly, pinnedOther),
			wantDropped: pulls(pinnedOnly, pinnedOther),
		},
		{
			name:     "same pinned image requested twice",
			pulls:    pulls(pinnedOnly, "docker.io/library/"+pinnedOnly),
			wantKept: pulls(pinnedOnly, "docker.io/library/"+pinnedOnly),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			kept, dropped := dropCollidingDigestPulls(tc.pulls, tc.loadNames)
			assert.Equal(t, tc.wantKept, kept)
			assert.Equal(t, tc.wantDropped, dropped)
		})
	}
}
