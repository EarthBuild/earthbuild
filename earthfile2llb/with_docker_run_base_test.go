package earthfile2llb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The environment variables built here are consumed by buildkitd/dockerd-wrapper.sh, which is
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
			expected: []string{`EARTHLY_DOCKERD_DATA_ROOT="/var/earthbuild/dind/` + dindID + `"`},
			notExpected: []string{
				`EARTHLY_DOCKERD_CACHE_DATA="true"`, `EARTHLY_START_COMPOSE="true"`,
			},
		},
		{
			name:   "cache id implies cache data",
			dindID: "cache_mycache",
			expected: []string{
				`EARTHLY_DOCKERD_DATA_ROOT="/var/earthbuild/dind/cache_mycache"`,
				`EARTHLY_DOCKERD_CACHE_DATA="true"`,
			},
		},
		{
			name:            "tar paths and digests",
			dindID:          dindID,
			tarPaths:        []string{"/tmp/a.tar", "/tmp/b.tar"},
			imgsWithDigests: []string{"alpine@sha256:aaa"},
			expected: []string{
				`EARTHLY_DOCKER_LOAD_FILES="/tmp/a.tar /tmp/b.tar"`,
				`EARTHLY_IMAGES_WITH_DIGESTS="alpine@sha256:aaa"`,
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
				`EARTHLY_START_COMPOSE="true"`,
				`EARTHLY_COMPOSE_FILES="docker-compose.yml override.yml"`,
				`EARTHLY_COMPOSE_SERVICES="db cache"`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			wrapFun := makeWithDockerdWrapFun(tc.dindID, tc.tarPaths, tc.imgsWithDigests, tc.opt)
			got := wrapFun([]string{"echo", "hello"}, nil, true, false, false)

			require.Len(t, got, 3)
			assert.Equal(t, []string{shellPath, "-c"}, got[:2])

			cmd := got[2]
			assert.Contains(t, cmd, dockerdWrapperPath+" execute")
			assert.Contains(t, cmd, "echo hello")

			for _, want := range tc.expected {
				assert.Contains(t, cmd, want)
			}

			for _, notWant := range tc.notExpected {
				assert.NotContains(t, cmd, notWant)
			}
		})
	}
}
