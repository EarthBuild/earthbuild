package interp_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `SAVE IMAGE --insecure` is accepted and ignored, because this engine does not
// push.
//
// The flag governs one thing: whether the *push* may talk to a registry over
// plain HTTP. An image built with it and an image built without it are the same
// image - the difference is in a transport that is never opened here, which is
// what `pushNote` already tells the operator about `--push` itself.
//
// So refusing it turned away an Earthfile over a flag that could not have
// changed the result, which is the mistake I5 exists to prevent. `--push` is the
// precedent: recorded as a declaration, and not acted on.
//
// **Not `--no-manifest-list`**, which stays refused. That one says what shape
// the artefact takes, and an engine that ignored it would hand back something
// other than what was asked for.
func TestAnInsecurePushFlagIsAcceptedAndIgnored(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    RUN build-it
    SAVE IMAGE --insecure --push app:latest
`, testMain)
	if err != nil {
		t.Fatalf("a flag that cannot change the result was refused: %v", err)
	}

	if len(p.Images) != 1 || p.Images[0].Ref != testImageRef {
		t.Fatalf("the image was not declared: %+v", p.Images)
	}

	if !p.Images[0].Push {
		t.Error("--push was not recorded; the declaration is what the operator" +
			" is told about, so losing it loses the note")
	}
}

// Ignoring it means ignoring it: the graph is the one the flag was absent from.
//
// Read from the other end, this is the same requirement - a build told it may
// push insecurely and a build not told must share cache entries, which they
// cannot do if the flag reaches the key.
func TestAnInsecurePushFlagDoesNotChangeTheGraph(t *testing.T) {
	t.Parallel()

	mk := func(src string) []ir.NodeID {
		p, err := interp.Build(versioned+src, testMain)
		if err != nil {
			t.Fatal(err)
		}

		ids := make([]ir.NodeID, 0, len(p.Graph.Nodes()))
		for _, n := range p.Graph.Nodes() {
			ids = append(ids, n.ID())
		}

		return ids
	}

	with := mk(`
main:
    FROM alpine:3.22
    RUN build-it
    SAVE IMAGE --insecure app:latest
`)
	without := mk(`
main:
    FROM alpine:3.22
    RUN build-it
    SAVE IMAGE app:latest
`)

	if len(with) != len(without) {
		t.Fatalf("the graphs differ in size: %d and %d", len(with), len(without))
	}

	for i := range with {
		if with[i] != without[i] {
			t.Errorf("node %d differs: an insecure-push flag reached the key", i)
		}
	}
}
