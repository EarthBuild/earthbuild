package interp_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

func execNodes(p *interp.Plan) []*ir.Node {
	var out []*ir.Node

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec {
			out = append(out, n)
		}
	}

	return out
}

// ENV sets a variable for the commands after it.
//
// It is ε - the ambient state a step may observe (green paper §3.4) - so it
// belongs to the operation and reaches the key. A variable that changed what a
// command did without changing its key is the same false hit as an edited COPY
// source.
func TestEnvReachesLaterSteps(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    ENV CGO_ENABLED=0
    RUN go build
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	steps := execNodes(p)
	if len(steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(steps))
	}

	if got := steps[0].Op.Env["CGO_ENABLED"]; got != "0" {
		t.Errorf("CGO_ENABLED is %q, want 0", got)
	}
}

// A variable declared after a step is not visible to it.
func TestEnvAppliesOnlyAfterItIsDeclared(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    RUN early
    ENV LATE=1
    RUN late
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range execNodes(p) {
		_, set := n.Op.Env["LATE"]

		if want := n.Op.Args[len(n.Op.Args)-1] == "late"; set != want {
			t.Errorf("%s: LATE set=%v, want %v", n.Meta.Description, set, want)
		}
	}
}

// Changing a variable changes the steps that can see it.
func TestEnvValuesReachTheKey(t *testing.T) {
	t.Parallel()

	mk := func(v string) ir.NodeID {
		p, err := interp.Build(versioned+"\nbuild:\n    FROM alpine\n    ENV FLAG="+v+"\n    RUN make\n", "build")
		if err != nil {
			t.Fatal(err)
		}

		return p.Graph.Root.ID()
	}

	if mk("on") == mk("off") {
		t.Error("two values of an environment variable produced the same step")
	}
}

// ENV and ARG differ: an argument is substituted into the command text, a
// variable is handed to the process. Both must work, and a variable must not be
// silently expanded at plan time.
func TestEnvIsNotExpandedIntoTheCommand(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    ENV NAME=value
    RUN echo $NAME
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	steps := execNodes(p)
	if len(steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(steps))
	}

	// The command still says $NAME: the shell expands it from the environment
	// the step is given, which is what makes ENV and ARG different things.
	if got := steps[0].Meta.Description; !contains(got, "$NAME") {
		t.Errorf("the variable was expanded at plan time: %s", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}

		return false
	})()
}
