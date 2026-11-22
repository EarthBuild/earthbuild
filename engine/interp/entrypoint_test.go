package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `RUN --entrypoint` runs the base image's own entrypoint with these arguments.
//
// It is how a build uses a tool image: `namely/protoc-all` is an image whose
// entrypoint *is* protoc, and `RUN --entrypoint -- -f api.proto -l go` means
// "run that, with these flags". Without it such an image can only be used by
// knowing what its entrypoint happens to be and writing it out by hand, which
// is the thing the image exists to avoid.
func TestEntrypointIsRecordedOnTheStep(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM namely/protoc-all:1.29_4
    RUN --entrypoint -- -f api.proto -l go
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpExec {
			continue
		}

		if !n.Op.Entrypoint {
			t.Error("the step does not ask for the image's entrypoint")
		}

		// Exec form: the arguments go to the entrypoint, not to a shell, and a
		// shell would re-split them.
		if len(n.Op.Args) == 0 || n.Op.Args[0] == testShell {
			t.Errorf("the arguments were handed to a shell: %v", n.Op.Args)
		}

		if strings.Join(n.Op.Args, " ") != "-f api.proto -l go" {
			t.Errorf("the arguments are %v", n.Op.Args)
		}

		return
	}

	t.Errorf("no step in the graph:\n%s", describe(p.Graph.Nodes()))
}

// Running the entrypoint is a different operation from running the same words
// as a command.
func TestEntrypointIsPartOfIdentity(t *testing.T) {
	t.Parallel()

	key := func(src string) ir.NodeID {
		t.Helper()

		p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
`+src, testMain)
		if err != nil {
			t.Fatal(err)
		}

		for _, n := range p.Graph.Nodes() {
			if n.Op.Kind == ir.OpExec {
				return n.ID()
			}
		}

		t.Fatal("no step")

		return ir.NodeID{}
	}

	if key("    RUN --entrypoint -- serve\n") == key("    RUN serve\n") {
		t.Error("running the entrypoint shares a key with running the words")
	}
}

// A step without the flag is unchanged: shell form, as every RUN is.
func TestAnOrdinaryRunIsStillShellForm(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    RUN echo hello > out.txt
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec {
			if n.Op.Entrypoint {
				t.Error("an ordinary RUN asked for the entrypoint")
			}

			if len(n.Op.Args) == 0 || n.Op.Args[0] != testShell {
				t.Errorf("an ordinary RUN lost its shell: %v", n.Op.Args)
			}

			return
		}
	}
}

// `RUN --entrypoint` with nothing after it runs the entrypoint bare.
//
// The corpus writes exactly that, and it is the natural form: an image whose
// entrypoint is a whole program needs no arguments to run it. Requiring a
// command refused a line whose meaning is complete without one.
func TestEntrypointNeedsNoArgumentsOfItsOwn(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    RUN --entrypoint
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec {
			if !n.Op.Entrypoint {
				t.Error("the step does not ask for the entrypoint")
			}

			if len(n.Op.Args) != 0 {
				t.Errorf("the step has arguments it was never given: %v", n.Op.Args)
			}

			return
		}
	}

	t.Errorf("no step in the graph:\n%s", describe(p.Graph.Nodes()))
}

// An ordinary RUN with nothing after it is still refused: there is no command
// and nothing to infer one from.
func TestAnEmptyRunIsStillRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    RUN
`, testMain)
	if err == nil {
		t.Fatal("an empty RUN was accepted")
	}
}
