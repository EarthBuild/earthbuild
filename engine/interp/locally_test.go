package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// LOCALLY makes the commands after it run on the invoking machine, unsandboxed.
//
// The specification calls this `host` and distinguishes it throughout: it is
// unsandboxed, non-cacheable, and never retried (I7). Those are not three
// policies, they are one fact - nothing bounds what it observed - stated three
// ways.
func TestLocallyMakesLaterStepsHostSteps(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    LOCALLY
    RUN ./scripts/release.sh
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	nodes := p.Graph.Nodes()
	if len(nodes) != 1 {
		t.Fatalf("got %d nodes, want 1:\n%s", len(nodes), describe(nodes))
	}

	if got := nodes[0].Op.Kind; got != ir.OpHost {
		t.Errorf("the step is %v, want a host step", got)
	}
}

// A LOCALLY target needs no base image: it runs on a machine that already
// exists. Requiring FROM would refuse an entire class of legitimate target.
func TestLocallyNeedsNoBaseImage(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+"\nbuild:\n    LOCALLY\n    RUN echo hi\n", "build")
	if err != nil {
		t.Fatalf("a LOCALLY target with no FROM was refused: %v", err)
	}
}

// A host step is still identified by what it does, so two different commands
// are two different steps.
func TestHostStepsAreStillIdentified(t *testing.T) {
	t.Parallel()

	mk := func(cmd string) ir.NodeID {
		p, err := interp.Build(versioned+"\nbuild:\n    LOCALLY\n    RUN "+cmd+"\n", "build")
		if err != nil {
			t.Fatal(err)
		}

		return p.Graph.Root.ID()
	}

	if mk("one") == mk("two") {
		t.Error("two host commands produced one step")
	}
}

// COPY inside a LOCALLY target has nowhere to copy *into*: the filesystem is
// the machine's own. Refused rather than quietly writing to the developer's
// disk, which is a surprise nobody wants from a build tool.
// Copying the *context* inside a LOCALLY target is refused - the file is
// already here. Copying an *artifact* is not, and has its own tests: the name
// of this one said "COPY inside LOCALLY is refused", which stopped being true
// and would have sent the next reader looking for a regression.
func TestCopyingTheContextInsideLocallyIsRefused(t *testing.T) {
	t.Parallel()

	ctx := ctxWith(t, map[string]string{testSourceDir: "x"})

	_, err := interp.Build(versioned+"\nbuild:\n    LOCALLY\n    COPY src /dst\n", "build",
		interp.WithContext(ctx))
	if err == nil {
		t.Fatal("COPY inside LOCALLY was accepted")
	}

	if !strings.Contains(err.Error(), "LOCALLY") {
		t.Errorf("the refusal does not mention LOCALLY:\n%s", err)
	}
}

// Once a target is LOCALLY it stays that way: a FROM afterwards would mean the
// steps before it ran on the host and the steps after in a sandbox, which is
// two targets wearing one name.
func TestFromAfterLocallyIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+"\nbuild:\n    LOCALLY\n    RUN x\n    FROM alpine\n", "build")
	if err == nil {
		t.Fatal("FROM after LOCALLY was accepted")
	}
}
