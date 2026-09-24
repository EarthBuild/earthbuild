package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `SAVE IMAGE` stamps the engine's own labels on what it names.
//
// `dev.earthly.version`, `dev.earthly.git-sha` and `dev.earthly.built-by` are
// the engine's statement about an image, and `refuseReservedLabel` exists to
// stop an author writing them - which only makes sense if the engine writes
// them itself. It did not: the native `SAVE IMAGE` took the running config and
// added nothing, so every image it produced carried `"Labels": null` where the
// other engine's carried three.
//
// Caught by `tests/with-docker-validate-labels`, one of the four WITH DOCKER
// jobs in E924, whose whole assertion is `jq -e '.[].Config.Labels'` - which
// exits 1 on null and so failed on the absence rather than on a wrong value.
func TestSaveImageStampsTheEngineLabels(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    SAVE IMAGE app:latest
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	if len(p.Images) != 1 {
		t.Fatalf("the image was not declared: %+v", p.Images)
	}

	for _, key := range []string{
		"dev.earthly.version", "dev.earthly.git-sha", "dev.earthly.built-by",
	} {
		if _, ok := p.Images[0].Config.Labels[key]; !ok {
			t.Errorf("SAVE IMAGE did not stamp %s: %v", key, p.Images[0].Config.Labels)
		}
	}
}

// `--without-earthly-labels` leaves them off, which is the point of it.
//
// The flag exists so an image can be byte-identical across engine versions: the
// stamped labels carry a version and a git sha, so an image that keeps them
// changes whenever the engine does. A build that asks for reproducibility gets
// no labels at all rather than empty ones - `jq` must report `null`, which is
// what the companion test in `with-docker-validate-labels` greps for.
func TestWithoutEarthlyLabelsLeavesThemOff(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    SAVE IMAGE --without-earthly-labels app:latest
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	if len(p.Images) != 1 {
		t.Fatalf("the image was not declared: %+v", p.Images)
	}

	for key := range p.Images[0].Config.Labels {
		if strings.HasPrefix(key, "dev.earthly.") {
			t.Errorf("--without-earthly-labels still stamped %s", key)
		}
	}
}
