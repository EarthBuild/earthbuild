package image_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// An image reference is written out in full, because that is the only form a
// runtime will match.
//
// `docker load` of a layout annotated only with `built-here:v1` produced an
// image that `docker images` listed - twice - and `docker image inspect` denied
// existed, and `docker run built-here:v1` tried to fetch from a registry that
// had never heard of it. Running it by ID worked, which is what proved the
// image was right and only its name was wrong.
func TestAReferenceIsNormalisedToItsFullForm(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, want string }{
		{"built-here:v1", "docker.io/library/built-here:v1"},
		{"built-here", "docker.io/library/built-here:latest"},
		{"myorg/app:2", "docker.io/myorg/app:2"},
		{"ghcr.io/org/app:1.0", "ghcr.io/org/app:1.0"},
		{"localhost:5000/app:1", "localhost:5000/app:1"},
		{"registry.example.com/team/app", "registry.example.com/team/app:latest"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := image.FullReference(tc.in); got != tc.want {
				t.Errorf("%q became %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A reference that is already a digest keeps it rather than gaining a tag.
func TestADigestReferenceIsLeftAlone(t *testing.T) {
	t.Parallel()

	const ref = "docker.io/library/alpine@sha256:" +
		"0000000000000000000000000000000000000000000000000000000000000000"

	if got := image.FullReference(ref); got != ref {
		t.Errorf("a digest reference became %q", got)
	}
}
