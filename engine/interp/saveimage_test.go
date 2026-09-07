package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// SAVE IMAGE names an image the target produces.
//
// Like SAVE ARTIFACT it is a declaration of output, not a step: it selects what
// a target is *for*, and adds nothing to the graph.
func TestSaveImageIsCollected(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    RUN make
    SAVE IMAGE myorg/tool:latest
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	if got := len(p.Graph.Nodes()); got != 2 {
		t.Errorf("graph has %d nodes, want 2; SAVE IMAGE is not a step", got)
	}

	if len(p.Images) != 1 {
		t.Fatalf("collected %d images, want 1", len(p.Images))
	}

	if got := p.Images[0].Ref; got != "myorg/tool:latest" {
		t.Errorf("image reference is %q", got)
	}
}

// Several tags for one image is ordinary.
func TestSaveImageAcceptsSeveralTags(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    SAVE IMAGE myorg/tool:latest myorg/tool:1.2.3
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	if len(p.Images) != 2 {
		t.Fatalf("collected %d images, want 2", len(p.Images))
	}
}

// An image is attributed to the step whose filesystem it names, so a later
// failure can say which command produced it.
func TestSaveImageRemembersItsStep(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    RUN make
    SAVE IMAGE tool:latest
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	if p.Images[0].From == nil {
		t.Fatal("the image is not attributed to a step")
	}

	if src := p.Images[0].From.Meta.Source; !strings.Contains(src, ":5") {
		t.Errorf("the image comes from %q, want the RUN at line 5", src)
	}
}

// Pushing is recorded rather than refused; see TestSaveImagePushIsRecorded.
//
// The test that stood here refused `--push` on the grounds that silently not
// pushing is a release that looks done and is not. That was the wrong reading of
// the flag: it declares an image *should* be published, and publishing happens
// when the invocation asks. Recording it is not ignoring it - a build that
// pushes nothing has been told to push nothing.
