package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPortOffset(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		installationName string
		want             int
	}{
		{
			// The ports derived from a zero offset are hardcoded in
			// earth-entrypoint.sh and buildkitd/buildkitd.tcp.template, so the
			// official installation name must not be offset.
			name:             "official name is not offset",
			installationName: "earth",
			want:             0,
		},
		{
			// "earthly" is also an official installation name and must map to the
			// same zero offset as "earth".
			name:             "deprecated official name is not offset",
			installationName: "earthly",
			want:             0,
		},
		{
			name:             "dev name is offset",
			installationName: "earthly-dev",
			want:             178,
		},
		{
			name:             "offset is stable for a given name",
			installationName: "earth-dev",
			want:             783,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, PortOffset(tc.installationName))
		})
	}
}

func TestPortOffsetIsInRange(t *testing.T) {
	t.Parallel()

	// Offsets must stay small enough that the derived ports remain valid, and
	// non-zero so a dev installation cannot collide with an official one.
	for _, name := range []string{"earthly-dev", "earth-dev", "a", "some-very-long-installation-name"} {
		offset := PortOffset(name)
		assert.GreaterOrEqual(t, offset, 10, name)
		assert.Less(t, offset, 1010, name)
	}
}
