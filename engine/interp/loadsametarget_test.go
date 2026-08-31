package interp_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Two targets that build the same filesystem still save two different images.
//
// A build graph deduplicates: `FROM alpine` plus the same COPY is one node
// however many targets write it, which is the point of a graph. The image a
// target saves is not a property of that node, though - `SAVE IMAGE
// --without-earthly-labels` and a plain one produce the same layers and
// different configurations - so resolving "the image this node saves" returns
// whichever was declared first, and one target is handed the other's image.
//
// Invisible until images could differ. Before SAVE IMAGE stamped the engine's
// labels the two configurations were identical, so picking the wrong one had no
// observable effect: `tests/with-docker-validate-labels` failed for what looked
// like a labels bug and was this (E926).
func TestTwoTargetsWithOneFilesystemSaveTwoImages(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
plain:
    FROM alpine:3.22
    SAVE IMAGE app:latest

bare:
    FROM alpine:3.22
    SAVE IMAGE --without-earthly-labels app:latest

use-plain:
    FROM alpine:3.22
    WITH DOCKER --load=+plain
        RUN docker run app:latest
    END

use-bare:
    FROM alpine:3.22
    WITH DOCKER --load=+bare
        RUN docker run app:latest
    END

main:
    BUILD +use-plain
    BUILD +use-bare
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var packed []*ir.Node

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpPackImage {
			packed = append(packed, n)
		}
	}

	if len(packed) != 2 {
		t.Fatalf("expected an image packed for each load, got %d", len(packed))
	}

	// **Two packs, two identities.** The archive a load reads is named from the
	// packing step's ID, so two packs that hash alike name one file - and the
	// second load reads the first's image whatever it asked for.
	if packed[0].ID() == packed[1].ID() {
		t.Errorf("both loads pack to one archive: %s", packed[0].ID())
	}

	withLabels := 0

	for _, n := range packed {
		if n.Op.Image == nil {
			continue
		}

		if _, ok := n.Op.Image.Labels["dev.earthly.version"]; ok {
			withLabels++
		}
	}

	// One target stamps the engine's labels and one asks not to, so exactly one
	// of the two packed images carries them. Both or neither means one load
	// took the other's configuration.
	if withLabels != 1 {
		t.Errorf("expected exactly one packed image to carry the engine labels, got %d", withLabels)
	}
}
