package interp_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `COPY --symlink-no-follow` is accepted, and it reaches the graph.
//
// Measured before implemented (E75). Varying the flag on one side at a time
// against the reference established that **the flag on the COPY is what
// decides**: with it, a link to a directory arrives as a link; without it, on
// either side, the tree arrives. The documentation's "the same flag must also
// be used in the corresponding COPY command" is that fact stated from the other
// end.
//
// The refusal was correct while nothing implemented it - a real feature, and
// accepting it silently would have produced a build that dereferenced where the
// author asked it not to. What made it implementable was knowing exactly which
// of the two sides carries the meaning.
func TestCopySymlinkNoFollowReachesTheGraph(t *testing.T) {
	t.Parallel()

	src := `VERSION 0.8

build:
    FROM alpine:3.22
    RUN mkdir real && ln -s real link
    SAVE ARTIFACT link

probe:
    FROM alpine:3.22
    COPY --symlink-no-follow +build/link got
`

	plan, err := interp.Build(src, "probe", interp.WithPlatform("linux/arm64"))
	if err != nil {
		t.Fatalf("the flag was refused:\n%v", err)
	}

	var found bool

	for _, n := range plan.Graph.Nodes() {
		if n.Op.Kind == ir.OpFile && n.Op.NoFollow {
			found = true
		}
	}

	if !found {
		t.Error("the plan has no copy that preserves the link, so the flag was accepted and dropped")
	}
}

// Two copies that differ only in the flag are two different steps.
//
// I3, and the one mistake here that would be expensive: a flag that changes
// what a step produces and does not change its key is a false cache hit - the
// failure this engine's whole cache design exists to make impossible. A build
// that ran the dereferencing form first would then serve its result to the
// build that asked for the link.
//
// Asserted on the *node identity* rather than on a field, because that is what
// the cache is keyed on, and a field added to the struct and forgotten in the
// hash is exactly how this goes wrong.
func TestTheFlagChangesTheStepIdentity(t *testing.T) {
	t.Parallel()

	id := func(flag string) ir.NodeID {
		t.Helper()

		src := `VERSION 0.8

build:
    FROM alpine:3.22
    RUN mkdir real && ln -s real link
    SAVE ARTIFACT link

probe:
    FROM alpine:3.22
    COPY ` + flag + ` +build/link got
`

		plan, err := interp.Build(src, "probe", interp.WithPlatform("linux/arm64"))
		if err != nil {
			t.Fatal(err)
		}

		for _, n := range plan.Graph.Nodes() {
			if n.Op.Kind == ir.OpFile {
				return n.ID()
			}
		}

		t.Fatal("no copy in the plan")

		return ir.NodeID{}
	}

	if id("") == id("--symlink-no-follow") {
		t.Error("a copy that preserves a link has the same identity as one that follows it" +
			"\n  the two produce different filesystems, so this is a false cache hit (I3)")
	}
}

// SAVE ARTIFACT accepts it, because a capture already does what it asks.
//
// A layer holds a symlink as a symlink, so nothing is dereferenced when an
// artifact is saved and there is nothing here for the flag to change. That
// alone would make refusing it merely pedantic; what makes it wrong is that the
// reference requires the flag on **both** the SAVE ARTIFACT and the COPY, so
// refusing it here makes the only spelling that works on the other engine
// unbuildable on this one.
//
// Which is `--keep-ts` again (E34): refusing a flag that asks for behaviour the
// engine already has costs a user a build that would have been correct. The
// first version of this feature refused it and the differential case could not
// be written for both engines - that is what found it.
func TestSaveArtifactAcceptsIt(t *testing.T) {
	t.Parallel()

	src := `VERSION 0.8

probe:
    FROM alpine:3.22
    RUN mkdir real && ln -s real link
    SAVE ARTIFACT --symlink-no-follow link
`

	_, err := interp.Build(src, "probe", interp.WithPlatform("linux/arm64"))
	if err != nil {
		t.Errorf("the flag was refused on SAVE ARTIFACT:\n%v", err)
	}
}
