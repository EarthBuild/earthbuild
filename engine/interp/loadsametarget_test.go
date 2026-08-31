package interp_test

import (
	"strings"
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

// Two blocks loading one image each load it, because each has its own daemon.
//
// A `--load` is two steps: packing an archive into the store, and running
// `docker load` against the block's daemon. The archive is content-addressed and
// daemon-independent, so two blocks wanting the same image should share the pack
// - that is the graph doing its job. The load is not: it mutates one daemon, and
// `dockerScope` names which. Without the scope on that node the two loads hash
// alike, deduplicate to one, and run against whichever daemon got there first -
// leaving the other block's empty and its step reporting `Unable to find image
// 'a:latest' locally` about an image the build had just made (E927).
func TestTwoBlocksEachLoadTheImage(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
img:
    FROM alpine:3.22
    SAVE IMAGE a:latest

first:
    FROM alpine:3.22
    WITH DOCKER --load=+img
        RUN docker run a:latest
    END

second:
    FROM alpine:3.22
    WITH DOCKER --load=+img
        RUN docker run a:latest
    END

main:
    BUILD +first
    BUILD +second
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	loads := 0
	packs := 0

	for _, n := range p.Graph.Nodes() {
		switch {
		case n.Op.Kind == ir.OpPackImage:
			packs++
		case n.Op.Kind == ir.OpExec && len(n.Op.Args) > 0 &&
			strings.Contains(strings.Join(n.Op.Args, " "), "docker load -i"):
			loads++
		}
	}

	if loads != 2 {
		t.Errorf("each block must load into its own daemon: got %d load steps, want 2", loads)
	}

	// The archive is the same file for both, and packing it twice would be
	// work for nothing. Sharing it is the graph being right, not the bug.
	if packs != 1 {
		t.Errorf("one archive serves both blocks: got %d pack steps, want 1", packs)
	}
}
