package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

const versioned = "VERSION 0.8\n"

// tryVersioned is a file that has opted into TRY/CATCH/FINALLY.
//
// A separate constant rather than adding the flag to `versioned`, because the
// gate is the point: a file that does not ask for the feature must not get it,
// and a shared constant carrying every flag would test the opposite.
const tryVersioned = "VERSION --try 0.8\n"

// A target becomes a chain: each command's input is the state before it.
func TestFromAndRunBecomeAChain(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    RUN echo one
    RUN echo two
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	nodes := p.Graph.Nodes()
	if len(nodes) != 3 {
		t.Fatalf("got %d nodes, want 3", len(nodes))
	}

	// Post-order: inputs before dependents, so the image is first.
	want := []ir.OpKind{ir.OpImage, ir.OpExec, ir.OpExec}
	for i, n := range nodes {
		if n.Op.Kind != want[i] {
			t.Errorf("node %d is %v, want %v", i, n.Op.Kind, want[i])
		}
	}

	if got := nodes[0].Op.Args[0]; got != testBaseImage {
		t.Errorf("image is %q", got)
	}
}

// Every node carries where it came from. Without this a diagnostic can say what
// failed but not where, and the first-divergence report has nothing to name.
func TestNodesCarryTheirSourceLocation(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+"\nbuild:\n    FROM alpine\n    RUN true\n", "build")
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Meta.Source == "" {
			t.Errorf("%v node has no source location", n.Op.Kind)

			continue
		}

		if !strings.Contains(n.Meta.Source, ":") {
			t.Errorf("source %q does not name a line", n.Meta.Source)
		}
	}
}

// A target that does not exist is a typo, and naming the alternatives is the
// difference between a two-second fix and a hunt.
func TestUnknownTargetNamesTheAlternatives(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+"\nbuild:\n    FROM alpine\n\ntest:\n    FROM alpine\n", "buidl")
	if err == nil {
		t.Fatal("an unknown target was accepted")
	}

	for _, want := range []string{"buidl", "build", "test"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%s", want, err)
		}
	}
}

// A target must start with FROM. Without a base there is no filesystem, and a
// RUN against nothing would fail deep in the executor with "no such file".
func TestRunBeforeFromIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+"\nbuild:\n    RUN echo hello\n", "build")
	if err == nil {
		t.Fatal("a RUN with no base image was accepted")
	}

	if !strings.Contains(err.Error(), "FROM") {
		t.Errorf("refusal does not mention FROM:\n%s", err)
	}
}

// The engine implements a subset, and says so per I10 - naming the construct,
// where it is, and what to do instead. Silently ignoring a command would build
// something that is not what the Earthfile describes.
func TestUnsupportedCommandsAreRefusedByName(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ src, name string }{
		// A bare WITH DOCKER is implemented; its options are not, and an option
		// accepted and ignored is worse than one refused - `--load` builds
		// another target and puts its image in the daemon, so a block that took
		// the flag and did nothing would run `docker run` against an image that
		// is not there.
		{"build:\n    FROM alpine\n    RUN --privileged true\n", testPrivilegedFlag},
		{"build:\n    FROM alpine\n    IF [ -f x ]\n        RUN true\n    END\n", "IF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+"\n"+tc.src, "build")
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}

			if !strings.Contains(err.Error(), tc.name) {
				t.Errorf("refusal does not name %s:\n%s", tc.name, err)
			}

			if !strings.Contains(err.Error(), "buildkit") {
				t.Errorf("refusal does not offer the alternative engine:\n%s", err)
			}
		})
	}
}

// Identical Earthfiles produce identical graphs, on any machine. The cache key
// is derived from node identity, so a graph that varied per run would never hit.
func TestGraphIdentityIsStable(t *testing.T) {
	t.Parallel()

	src := versioned + "\nbuild:\n    FROM alpine:3.22\n    RUN make\n"

	a, err := interp.Build(src, "build")
	if err != nil {
		t.Fatal(err)
	}

	b, err := interp.Build(src, "build")
	if err != nil {
		t.Fatal(err)
	}

	if a.Graph.Root.ID() != b.Graph.Root.ID() {
		t.Errorf("two parses of one Earthfile differ:\n%s\n%s", a.Graph.Root.ID(), b.Graph.Root.ID())
	}
}

// RUN is shell form: the arguments are a command line, not an argv.
//
// The parser hands them over with quoting intact, so `RUN sh -c "echo hi > f"`
// arrives as four tokens of which the last still has its quotes. Passing that
// straight to execve looks right and fails with exit 127, because the literal
// quoted string is not a program.
func TestRunIsShellForm(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+"\nbuild:\n    FROM alpine\n    RUN echo hi > /f\n", "build")
	if err != nil {
		t.Fatal(err)
	}

	n := p.Graph.Root
	if got := n.Op.Args[0]; got != testShell {
		t.Errorf("argv starts with %q, want /bin/sh: RUN is interpreted by a shell", got)
	}

	if len(n.Op.Args) != 3 || n.Op.Args[1] != "-c" {
		t.Fatalf("argv is %q, want [/bin/sh -c <command>]", n.Op.Args)
	}

	// The redirection must survive into the command line, or the shell has
	// nothing to interpret.
	if !strings.Contains(n.Op.Args[2], "> /f") {
		t.Errorf("command line is %q, want the redirection preserved", n.Op.Args[2])
	}
}

// Exec form bypasses the shell, which is what it is for.
func TestExecFormBypassesTheShell(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+"\nbuild:\n    FROM alpine\n    RUN [\"/bin/true\"]\n", "build")
	if err != nil {
		t.Fatal(err)
	}

	if got := p.Graph.Root.Op.Args[0]; got != "/bin/true" {
		t.Errorf("argv starts with %q, want /bin/true with no shell", got)
	}
}
