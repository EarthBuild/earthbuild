package interp_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// FROM +target uses another target's final filesystem as this one's base.
func TestFromAnotherTarget(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
deps:
    FROM alpine:3.22
    RUN apk add make

build:
    FROM +deps
    RUN make
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	nodes := p.Graph.Nodes()

	// alpine, apk add, make - the referenced target's steps are part of this
	// graph rather than a separate build.
	if len(nodes) != 3 {
		t.Fatalf("graph has %d nodes, want 3:\n%s", len(nodes), describe(nodes))
	}

	if got := nodes[len(nodes)-1].Meta.Description; !strings.Contains(got, "make") {
		t.Errorf("the last step is %q, want RUN make", got)
	}
}

// A target referenced twice is built once.
//
// Targets form a DAG, not a tree. Expanding a shared dependency per reference
// would build it as many times as it is named - which is the difference between
// a build tool and a shell script, and is where parallelism starts paying.
func TestSharedTargetsAreBuiltOnce(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
common:
    FROM alpine:3.22
    RUN expensive

left:
    FROM +common
    RUN left-thing

right:
    FROM +common
    RUN right-thing

all:
    BUILD +left
    BUILD +right
`, "all")
	if err != nil {
		t.Fatal(err)
	}

	var expensive int

	for _, n := range p.Graph.Nodes() {
		if strings.Contains(n.Meta.Description, "expensive") {
			expensive++
		}
	}

	if expensive != 1 {
		t.Errorf("the shared step appears %d times, want 1:\n%s", expensive, describe(p.Graph.Nodes()))
	}
}

// A cycle must be refused, naming the loop.
//
// Without this the interpreter recurses until the stack runs out, and a stack
// overflow names nothing at all - least of all which two targets refer to each
// other.
func TestCyclesAreRefusedByName(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
a:
    FROM +b

b:
    FROM +a
`, "a")
	if err == nil {
		t.Fatal("a cycle between two targets was accepted")
	}

	for _, want := range []string{"+a", "+b", "cycle"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%s", want, err)
		}
	}
}

// A target referring to itself is the same defect, one step shorter.
func TestSelfReferenceIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+"\nloop:\n    FROM +loop\n", "loop")
	if err == nil {
		t.Fatal("a self-referencing target was accepted")
	}

	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error does not name the cycle:\n%s", err)
	}
}

// A reference to a target that does not exist lists what does.
func TestUnknownTargetReferenceListsAlternatives(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+"\nbuild:\n    FROM +dpes\n\ndeps:\n    FROM alpine\n", "build")
	if err == nil {
		t.Fatal("a reference to a missing target was accepted")
	}

	for _, want := range []string{"dpes", "deps"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%s", want, err)
		}
	}
}

// BUILD +target makes this target depend on another without changing its own
// filesystem: it is a dependency edge, not a base.
func TestBuildIsADependencyNotABase(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
dep:
    FROM alpine:3.22
    RUN side-effect

main:
    FROM alpine:3.22
    BUILD +dep
    RUN main-thing
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var main *ir.Node

	for _, n := range p.Graph.Nodes() {
		if strings.Contains(n.Meta.Description, "main-thing") {
			main = n
		}
	}

	if main == nil {
		t.Fatal("the main step is missing")
	}

	// RUN main-thing stands on alpine, not on the dependency's filesystem.
	for _, in := range main.Inputs {
		if strings.Contains(in.Meta.Description, "side-effect") {
			t.Error("BUILD placed the dependency in the base; it is a dependency edge, not a base")
		}
	}

	// But the dependency is still in the graph, so it still runs.
	var found bool

	for _, n := range p.Graph.Nodes() {
		if strings.Contains(n.Meta.Description, "side-effect") {
			found = true
		}
	}

	if !found {
		t.Error("BUILD +dep did not put the dependency in the graph")
	}
}

func describe(nodes []*ir.Node) string {
	var b strings.Builder

	for _, n := range nodes {
		b.WriteString("  " + n.Meta.Source + " " + n.Meta.Description + "\n")
	}

	return b.String()
}

// BUILD has no refused flag left, and that is written here rather than asserted
// with a substitute.
//
// This was `TestBuildFlagsAreRefusedNotIgnored`, and it held two:
// `--allow-privileged` until it was accepted (E476), `--auto-skip` until it was
// (E484). Nothing remains for it to be about.
//
// `TestNoFlagIsSilentlyDropped` watches this command's flags now - all of them,
// rather than the two somebody remembered - and both departures are recorded on
// its known-dropped list with the reason each was accepted. The second time this
// has happened to a whole test in nine increments, which is what a list-based
// guard is for.

// `BUILD --platform` is honoured rather than refused, and the platform reaches
// the graph.
//
// It was once refused, on the reasoning that accepting the line and dropping the
// flag would build the wrong architecture and report success. That reasoning was
// right and the refusal is no longer how it is answered: the platform travels
// into the resolved target, lands on every node, and is part of each node's key.
// A worker that cannot satisfy it is then a scheduling failure, which says so,
// rather than a silent build of the wrong thing.
func TestBuildPlatformReachesTheGraph(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
dep:
    FROM alpine
    RUN in-the-dependency

main:
    FROM alpine
    BUILD --platform=linux/amd64 +dep
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var found bool

	for _, n := range p.Graph.Nodes() {
		if !strings.Contains(n.Meta.Description, "in-the-dependency") {
			continue
		}

		found = true

		if got := (ir.Platform{OS: n.Platform.OS, Arch: n.Platform.Arch}); got != (ir.Platform{OS: testOS, Arch: testArch}) {
			t.Errorf("the step runs on %+v, want linux/amd64: --platform was dropped", got)
		}
	}

	if !found {
		t.Error("the dependency's step is not in the graph at all")
	}
}

// A local context is read from, never stood on.
//
// The scheduler enforces this structurally - Sources are keyed but not stacked -
// so the guarantee holds only if the interpreter puts a context there. Nothing
// else checks that it does, and putting it in Inputs instead would merge the
// developer's own directory layout into the image while every other test stayed
// green.
func TestALocalContextIsASourceNotAnInput(t *testing.T) {
	t.Parallel()

	ctx := ctxWith(t, map[string]string{testSourcePath: "package main\n"})

	p, err := interp.Build(versioned+"\nmain:\n    FROM alpine\n    COPY src/main.go /app/\n",
		testMain, interp.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}

	var checked bool

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpFile {
			continue
		}

		checked = true

		for _, in := range n.Inputs {
			if in.Op.Kind == ir.OpLocal {
				t.Error("the build context is an Input of COPY, so it would be stacked")
			}
		}

		var fromContext bool

		for _, src := range n.Sources {
			fromContext = fromContext || src.Op.Kind == ir.OpLocal
		}

		if !fromContext {
			t.Error("the build context is not a Source of COPY, so it is not in the key")
		}
	}

	if !checked {
		t.Fatal("no COPY in the graph")
	}
}

// A cross-file reference is resolved now, so one naming a directory with no
// Earthfile says where it looked rather than reporting a malformed reference.
func TestCrossFileReferenceToAMissingEarthfile(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+"\nmain:\n    FROM alpine\n    BUILD ./examples/c+docker\n", testMain)
	if err == nil {
		t.Fatal("a reference to a directory with no Earthfile was accepted")
	}

	if !strings.Contains(err.Error(), testEarthfile) {
		t.Errorf("the error does not say what it looked for:\n%s", err)
	}
}

// The repository keeps an Earthfile that exists to contain infinite recursion,
// as a fixture for the engine that already ships. This engine finds the same
// cycles in it - including the three-hop one - which is an independent check on
// the detector that no test written alongside it can give.
func TestTheRecursionFixtureIsDetected(t *testing.T) {
	t.Parallel()

	dir := os.Getenv("EARTH_CORPUS_DIR")
	if dir == "" {
		dir = "../.."
	}

	path := filepath.Join(dir, "tests", "cli", "testdata", "infinite-recursion", testEarthfile)

	src, err := os.ReadFile(path) // a fixture this test wrote
	if err != nil {
		t.Skipf("the recursion fixture is not here: %v", err)
	}

	for _, target := range []string{"test1", "test2", "test3"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(string(src), target, interp.WithContext(filepath.Dir(path)))
			if err == nil {
				t.Fatal("a target the fixture defines as recursive was accepted")
			}

			var cycle *interp.CycleError
			if !errors.As(err, &cycle) {
				t.Fatalf("refused, but not as a cycle: %v", err)
			}

			// The loop must name the target it returns to, or it says nothing
			// about where to break the chain.
			if len(cycle.Loop) < 2 || cycle.Loop[0] != cycle.Loop[len(cycle.Loop)-1] {
				t.Errorf("the loop does not close: %v", cycle.Loop)
			}
		})
	}
}
