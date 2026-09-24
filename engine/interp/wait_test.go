package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A WAIT block finishes before anything after it starts.
//
// Everything in a target is already sequential, so the interesting case is the
// one that is not: a BUILD inside the block is a dependency edge rather than a
// base, and without WAIT nothing makes the step after it wait for that build.
func TestWaitOrdersWhatFollowsIt(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
dep:
    FROM alpine:3.22
    RUN the-dependency

main:
    FROM alpine:3.22
    WAIT
        BUILD +dep
    END
    RUN after-the-wait
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var after, dep *ir.Node

	for _, n := range p.Graph.Nodes() {
		switch {
		case strings.Contains(n.Meta.Description, "after-the-wait"):
			after = n
		case strings.Contains(n.Meta.Description, "the-dependency"):
			dep = n
		}
	}

	if after == nil || dep == nil {
		t.Fatalf("the graph is missing a step:\n%s", describe(p.Graph.Nodes()))
	}

	var ordered bool

	for _, a := range after.After {
		if a.ID() == dep.ID() {
			ordered = true
		}
	}

	if !ordered {
		t.Error("the step after the block does not wait for what the block built")
	}

	// Waited for, not stood on: the dependency's filesystem is not this step's.
	for _, in := range after.Inputs {
		if in.ID() == dep.ID() {
			t.Error("the step after the block stands on the dependency")
		}
	}
}

// The steps inside a WAIT are ordinary steps of the target.
func TestWaitRunsItsBody(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WAIT
        RUN inside-the-block
    END
    RUN after
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	for _, want := range []string{"inside-the-block", "after"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is not in the graph:\n%s", want, got)
		}
	}
}

// A WAIT block changes when work happens, not what is produced, so the steps
// around it keep their identities.
func TestWaitDoesNotChangeWhatIsBuilt(t *testing.T) {
	t.Parallel()

	mk := func(src string) string {
		p, err := interp.Build(versioned+src, testMain)
		if err != nil {
			t.Fatal(err)
		}

		var b strings.Builder

		for _, n := range p.Graph.Nodes() {
			if n.Op.Kind == ir.OpExec {
				b.WriteString(n.ID().String() + "\n")
			}
		}

		return b.String()
	}

	plain := mk("\nmain:\n    FROM alpine:3.22\n    RUN one\n    RUN two\n")
	waited := mk("\nmain:\n    FROM alpine:3.22\n    WAIT\n        RUN one\n    END\n    RUN two\n")

	if plain != waited {
		t.Errorf("a WAIT changed the identity of the work around it:\n%s\n%s", plain, waited)
	}
}
