package image_test

import (
	"reflect"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// TestMirrorsAreReadFromTheEnvironment.
//
// Off unless asked: a mirror answers "what does this tag mean" from its own
// cache, so turning one on by default would change which image a build gets
// without anybody saying so. Configured, it applies to Docker Hub - the registry
// that rate-limits, and the one every mirror in existence fronts.
//
//nolint:paralleltest // t.Setenv, which the runtime refuses in a parallel test
func TestMirrorsAreReadFromTheEnvironment(t *testing.T) {
	for _, c := range []struct {
		set  string
		want map[string][]string
	}{
		{"", nil},
		{"   ", nil},
		{"mirror.gcr.io", map[string][]string{"docker.io": {"mirror.gcr.io"}}},
		{
			" mirror.gcr.io , public.ecr.aws ,, ",
			map[string][]string{"docker.io": {"mirror.gcr.io", "public.ecr.aws"}},
		},
		// A scheme is what a person writes and not what a host is; taking it
		// literally builds `https://https://mirror.gcr.io/v2/...`.
		{"https://mirror.gcr.io/", map[string][]string{"docker.io": {"mirror.gcr.io"}}},
	} {
		t.Setenv(image.EnvMirrors, c.set)

		got := image.MirrorsFromEnv()
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q gave %v, want %v", c.set, got, c.want)
		}
	}
}
