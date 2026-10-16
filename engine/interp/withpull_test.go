package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `--pull` puts an image in the daemon before the block's own commands run.
//
// Expressed as a step at the top of the block rather than as a property of it,
// because that is what it is: fetching an image is work, it can fail, and it has
// to happen before anything that uses the image. A step is also how it reaches
// the key - the body stands on it, so what was pulled is part of what the body
// is.
func TestPullPutsAStepAtTheTopOfTheBlock(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WITH DOCKER --pull alpine:3.22
        RUN docker run --rm alpine:3.22 true
    END
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var body *ir.Node

	for _, n := range p.Graph.Nodes() {
		if strings.Contains(n.Meta.Description, "docker run") {
			body = n
		}
	}

	if body == nil {
		t.Fatalf("the block's command is not in the graph:\n%s", describe(p.Graph.Nodes()))
	}

	if !reaches(body, "docker pull alpine:3.22") {
		t.Errorf("the body does not stand on the pull:\n%s", describe(p.Graph.Nodes()))
	}
}

// The pull needs a daemon as much as anything else in the block does.
func TestAPullStepAsksForADaemon(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WITH DOCKER --pull alpine:3.22
        RUN docker images
    END
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if strings.Contains(n.Meta.Description, "docker pull") && !n.Op.Docker {
			t.Error("the pull runs without a daemon to pull into")
		}
	}
}

// Several pulls all happen, in the order written.
//
// Order matters less than completeness here, but a pull that was silently
// dropped would surface as `docker run` failing to find an image the Earthfile
// clearly asked for.
func TestEveryPullHappens(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WITH DOCKER --pull alpine:3.22 --pull busybox:1.36
        RUN docker images
    END
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, ref := range []string{testBaseImage, "busybox:1.36"} {
		var found bool

		for _, n := range p.Graph.Nodes() {
			if n.Meta.Description == "docker pull "+ref {
				found = true
			}
		}

		if !found {
			t.Errorf("%s was never pulled:\n%s", ref, describe(p.Graph.Nodes()))
		}
	}
}

// Two blocks pulling different images are different builds.
//
// The block is uncacheable today, so this is not yet load-bearing - but the key
// has to be right before the block becomes cacheable, not afterwards, because a
// key that was wrong while nothing read it is a cache poisoned the moment
// something does.
func TestWhatWasPulledIsPartOfTheBodysIdentity(t *testing.T) {
	t.Parallel()

	key := func(ref string) ir.NodeID {
		t.Helper()

		p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WITH DOCKER --pull `+ref+`
        RUN docker images
    END
`, testMain)
		if err != nil {
			t.Fatal(err)
		}

		for _, n := range p.Graph.Nodes() {
			if n.Meta.Description == testDockerImages {
				return n.ID()
			}
		}

		t.Fatal("the body is not in the graph")

		return ir.NodeID{}
	}

	if key(testBaseImage) == key("busybox:1.36") {
		t.Error("a body that ran against two different images has one key")
	}
}
