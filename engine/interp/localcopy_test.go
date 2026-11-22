package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `COPY +target/artifact <path>` inside a LOCALLY target puts the artifact on
// this machine, as a step in the target's chain.
//
// The refusal it replaces said there was no image to copy into, which was true
// and beside the point: the author did not ask for an image, they asked for a
// directory on the machine the target already runs on. Every COPY inside a
// LOCALLY target in this repository is this shape.
//
// **In the chain, not at the end.** It was planned as an `AS LOCAL` export,
// which the CLI writes once the build is over, so the `RUN tar xf` on the next
// line ran first, found nothing, and its failure cancelled the producer
// mid-build (`+buck2-regen`, and six targets in tests/local).
func TestCopyInsideLocallyIsAHostStep(t *testing.T) {
	t.Parallel()

	cp := hostCopyIn(t, "COPY +producer/out.txt ./collected.txt\n    RUN cat collected.txt")

	if strings.Join(cp.Op.Args, " ") != "/out.txt ./collected.txt out.txt" {
		t.Errorf("the copy is %q, want the artifact the target saved, the destination and its name", cp.Op.Args)
	}

	if len(cp.Sources) != 1 || cp.Sources[0].Meta.Description != "RUN make-it > /out.txt" {
		t.Errorf("the copy reads %s, want the producing step", describe(cp.Sources))
	}
}

// The RUN after the COPY stands on it, so it runs after the file has landed.
func TestTheRunAfterALocallyCopyStandsOnIt(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+producer+"\nmain:\n    LOCALLY\n"+
		"    COPY +producer/out.txt ./collected.txt\n    RUN cat collected.txt\n", testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Meta.Description == "RUN cat collected.txt" {
			if len(n.Inputs) != 1 || n.Inputs[0].Op.Kind != ir.OpHost || len(n.Inputs[0].Sources) != 1 {
				t.Fatalf("the RUN stands on %s, want the copy", describe(n.Inputs))
			}

			return
		}
	}

	t.Fatalf("no RUN in the graph:\n%s", describe(p.Graph.Nodes()))
}

// A destination outside the project is allowed here, and the reason is worth
// stating.
//
// `SAVE ARTIFACT AS LOCAL /etc/passwd` is refused because an Earthfile - which
// may have been fetched from elsewhere - must not choose where to write on
// someone's machine. A LOCALLY target is already running arbitrary commands on
// that machine: `RUN cp x /etc/passwd` is the same act with more steps. Refusing
// the copy while allowing the command would be theatre, and the real hazard -
// remote code with a LOCALLY target - is older and larger than this line.
func TestCopyInsideLocallyMayWriteOutsideTheProject(t *testing.T) {
	t.Parallel()

	cp := hostCopyIn(t, "COPY +producer/out.txt /tmp/somewhere-else.txt")

	if got := cp.Op.Args[1]; got != "/tmp/somewhere-else.txt" {
		t.Errorf("copied to %q", got)
	}
}

// Several sources go into the destination as a directory, one step each.
func TestALocallyCopyOfSeveralSourcesNamesADirectory(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+producer+"\nmain:\n    LOCALLY\n"+
		"    COPY +producer/out.txt +producer/out.txt /tmp/d\n", testMain)
	if err != nil {
		t.Fatal(err)
	}

	copies := 0

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpHost && len(n.Sources) == 1 {
			copies++

			if n.Op.Args[1] != "/tmp/d/" {
				t.Errorf("a copy of several sources writes to %q, want the directory /tmp/d/", n.Op.Args[1])
			}
		}
	}

	if copies != 2 {
		t.Errorf("%d copy steps, want one per source", copies)
	}
}

const producer = `
producer:
    FROM alpine:3.22
    RUN make-it > /out.txt
    SAVE ARTIFACT /out.txt
`

// hostCopyIn plans a LOCALLY target holding body and returns its copy step.
func hostCopyIn(t *testing.T, body string) *ir.Node {
	t.Helper()

	p, err := interp.Build(versioned+producer+"\nmain:\n    LOCALLY\n    "+body+"\n", testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpHost && len(n.Sources) > 0 {
			return n
		}
	}

	t.Fatalf("no host copy step in the graph:\n%s", describe(p.Graph.Nodes()))

	return nil
}

// A COPY from the build context inside a LOCALLY target is still refused.
//
// The file is already on this machine, at the path the line names, so the copy
// is from a directory to itself. Silently doing nothing would be worse than
// saying so.
func TestCopyingTheContextNamesWhatIsWrong(t *testing.T) {
	t.Parallel()

	ctx := ctxWith(t, map[string]string{testSourceFile: "x\n"})

	_, err := interp.Build(versioned+
		"\nmain:\n    LOCALLY\n    COPY src.txt ./elsewhere.txt\n", testMain, interp.WithContext(ctx))
	if err == nil {
		t.Fatal("copying the context onto itself was accepted")
	}

	if !strings.Contains(err.Error(), "LOCALLY") {
		t.Errorf("the refusal does not explain itself:\n%s", err)
	}
}
