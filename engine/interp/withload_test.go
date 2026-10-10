package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

const loadSrc = versioned + `
app:
    FROM alpine:3.22
    RUN build-the-app
    SAVE IMAGE app:latest

main:
    FROM alpine:3.22
    WITH DOCKER --load app:latest=+app
        RUN docker run --rm app:latest
    END
`

// `--load` builds the referenced target and puts its image in the daemon.
//
// 480 of the corpus's 892 WITH DOCKER lines, and the only one that makes the
// construct worth having: it is how a build tests the image it has just made.
func TestLoadBuildsTheTargetAndPacksIt(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(loadSrc, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var pack *ir.Node

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpPackImage {
			pack = n
		}
	}

	if pack == nil {
		t.Fatalf("nothing packs an image:\n%s", describe(p.Graph.Nodes()))
	}

	if len(pack.Op.Args) == 0 || pack.Op.Args[0] != testImageRef {
		t.Errorf("the image is packed as %v, want app:latest", pack.Op.Args)
	}

	// It stands on the target it names, or it would pack whatever happened to
	// be beneath it.
	if !reaches(pack, "RUN build-the-app") {
		t.Errorf("the packed image is not the referenced target's:\n%s", describe(p.Graph.Nodes()))
	}
}

// The body runs after the load, and is keyed on it.
func TestTheBodyStandsOnTheLoad(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(loadSrc, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if !strings.Contains(n.Meta.Description, "docker run") {
			continue
		}

		if !reaches(n, "docker load app:latest") {
			t.Errorf("the body does not stand on the load:\n%s", describe(p.Graph.Nodes()))
		}

		return
	}

	t.Errorf("the body is not in the graph:\n%s", describe(p.Graph.Nodes()))
}

// Without an explicit name, the target's own image name is used.
//
// `--load +app` means "the image that target saves". Inventing a name instead
// would load something under a tag the Earthfile never mentions, and the
// `docker run app:latest` two lines below would fail to find it.
func TestLoadWithoutANameUsesTheTargetsOwn(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
app:
    FROM alpine:3.22
    SAVE IMAGE app:latest

main:
    FROM alpine:3.22
    WITH DOCKER --load +app
        RUN docker run --rm app:latest
    END
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpPackImage {
			if len(n.Op.Args) == 0 || n.Op.Args[0] != testImageRef {
				t.Errorf("packed as %v, want the target's own app:latest", n.Op.Args)
			}

			return
		}
	}

	t.Errorf("nothing packs an image:\n%s", describe(p.Graph.Nodes()))
}

// A target that saves no image cannot be loaded, and says so.
func TestLoadingATargetWithNoImageIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
app:
    FROM alpine:3.22
    RUN build-the-app

main:
    FROM alpine:3.22
    WITH DOCKER --load +app
        RUN docker images
    END
`, testMain)
	if err == nil {
		t.Fatal("a target that saves no image was loaded")
	}

	for _, want := range []string{"+app", testCmdSaveImage} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, err)
		}
	}
}

// Two loads of different images are different builds.
func TestWhatWasLoadedIsPartOfTheBodysIdentity(t *testing.T) {
	t.Parallel()

	key := func(name string) ir.NodeID {
		t.Helper()

		p, err := interp.Build(versioned+`
app:
    FROM alpine:3.22
    SAVE IMAGE app:latest

main:
    FROM alpine:3.22
    WITH DOCKER --load `+name+`=+app
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

	if key("one:latest") == key("two:latest") {
		t.Error("a body given two different images has one key")
	}
}
