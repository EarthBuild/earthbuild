package buildkitd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Also covers the deprecated EARTHLY_WITH_DOCKER fallback in isEarthInEarth;
// drop those cases once EARTHLY_ support is officially removed.
func TestIsEarthInEarth(t *testing.T) {
	const (
		current    = "EARTH_WITH_DOCKER"
		deprecated = "EARTHLY_WITH_DOCKER"
	)

	for _, tc := range []struct {
		env  map[string]string
		name string
		want bool
	}{
		{
			name: "unset",
			want: false,
		},
		{
			// What dockerd-wrapper.sh exports.
			name: "current name",
			env:  map[string]string{current: "1"},
			want: true,
		},
		{
			// An older buildkitd image still exports the old spelling; it must
			// keep working, otherwise the cgroup bind-mount is silently lost.
			name: "deprecated name",
			env:  map[string]string{deprecated: "1"},
			want: true,
		},
		{
			name: "current name wins over deprecated",
			env:  map[string]string{current: "0", deprecated: "1"},
			want: false,
		},
		{
			name: "explicit false",
			env:  map[string]string{current: "false"},
			want: false,
		},
		{
			name: "unparseable value is not earth-in-earth",
			env:  map[string]string{current: "yes please"},
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			assert.Equal(t, tc.want, isEarthInEarth())
		})
	}
}
