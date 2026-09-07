package interp_test

import (
	"slices"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A loaded target's own declarations reach the image.
//
// `WITH DOCKER --load=name=+target` packs a target that need not have declared
// a `SAVE IMAGE`, and the config was read only from images a `SAVE IMAGE`
// named - so an `EXPOSE` or an `ENV` on the loaded target reached nothing. The
// image loaded with no ports and no environment of its own, and
// `tests/with-docker-expose` says so in as many words: it inspects
// `.Config.ExposedPorts` and diffs (E779).
func TestALoadedTargetsOwnConfigReachesTheImage(t *testing.T) {
	t.Parallel()

	plan, err := interp.Build(`VERSION 0.8
single:
    FROM alpine:3.24.1
    EXPOSE 1234
    ENV WHO=me
wd:
    FROM alpine:3.24.1
    WITH DOCKER --load=test:img=+single
        RUN true
    END
`, "wd")
	if err != nil {
		t.Fatal(err)
	}

	var (
		packs  int
		packed *ir.ImageConfig
	)

	for _, n := range plan.Graph.Nodes() {
		if n.Op.Kind == ir.OpPackImage {
			packs++
			packed = n.Op.Image
		}
	}

	if packs != 1 {
		t.Fatalf("%d steps pack an image, want 1", packs)
	}

	if packed == nil {
		t.Fatal("the image is packed with no configuration at all, so everything" +
			" the loaded target declared is lost")
	}

	if len(packed.Exposed) != 1 || packed.Exposed[0] != "1234/tcp" {
		t.Errorf("the packed image exposes %v, want the target's [1234/tcp]", packed.Exposed)
	}

	if !slices.Contains(packed.Env, "WHO=me") {
		t.Errorf("the packed image's env is %v, want the target's WHO=me", packed.Env)
	}
}
