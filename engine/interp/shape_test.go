package interp_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

const shapeEarthfile = `VERSION 0.8

build:
    FROM scratch
    COPY src.txt /
    RUN echo hello
`

// shapeProject writes an Earthfile and one context file, and plans it.
func shapePlan(t *testing.T, body, content string, opts ...interp.Option) *interp.Plan {
	t.Helper()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "src.txt"), []byte(content), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := interp.Build(body, "build", append([]interp.Option{interp.WithContext(dir)}, opts...)...)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	return plan
}

func localContentOf(t *testing.T, plan *interp.Plan) []ir.NodeID {
	t.Helper()

	var out []ir.NodeID

	for _, n := range plan.Graph.Nodes() {
		if n.Op.Kind == ir.OpLocal {
			out = append(out, n.Op.Content)
		}
	}

	if len(out) == 0 {
		t.Fatal("the plan has no context node, so this test measures nothing")
	}

	return out
}

// **A plan that does not digest the context.**
//
// The shape of a build - its commands, arguments, base images and what it
// produces - without what the copied files happen to contain. It is what a
// job-level skip keys on beside the files a build actually read
// (docs-internals/job-skipping.md), and it is *cheaper* than the ordinary plan
// rather than dearer: digesting the context is the largest thing planning does.
func TestAStubbedPlanIgnoresWhatTheContextHolds(t *testing.T) {
	t.Parallel()

	one := shapePlan(t, shapeEarthfile, "one", interp.WithoutContextDigests())
	two := shapePlan(t, shapeEarthfile, "a different length entirely",
		interp.WithoutContextDigests())

	if one.Graph.Root.ID() != two.Graph.Root.ID() {
		t.Error("two contexts differing only in content gave different plans")
	}
}

// And everything that is not content still moves it, or the shape is not a key
// at all - it would be equal for two builds running different commands.
func TestAStubbedPlanStillSeesTheEarthfile(t *testing.T) {
	t.Parallel()

	one := shapePlan(t, shapeEarthfile, "one", interp.WithoutContextDigests())
	two := shapePlan(t, shapeEarthfile+"    RUN echo again\n", "one",
		interp.WithoutContextDigests())

	if one.Graph.Root.ID() == two.Graph.Root.ID() {
		t.Error("an added command left the stubbed plan equal")
	}
}

// The stub is not the digest of anything, so it cannot collide with a real
// context, and an ordinary plan is unaffected by the option existing.
func TestAnOrdinaryPlanStillDigestsTheContext(t *testing.T) {
	t.Parallel()

	one := shapePlan(t, shapeEarthfile, "one")
	two := shapePlan(t, shapeEarthfile, "two")

	if one.Graph.Root.ID() == two.Graph.Root.ID() {
		t.Error("without the option, a changed context file left the plan equal")
	}

	stubbed := localContentOf(t, shapePlan(t, shapeEarthfile, "one",
		interp.WithoutContextDigests()))
	digested := localContentOf(t, one)

	if stubbed[0] == digested[0] {
		t.Error("the stub is the digest the context actually has")
	}

	if stubbed[0] == (ir.NodeID{}) {
		t.Error("the stub is the zero digest, which is what an empty tree hashes to")
	}
}
