package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

func descriptions(p *interp.Plan) string {
	var b strings.Builder

	for _, n := range p.Graph.Nodes() {
		b.WriteString(n.Meta.Description + "\n")
	}

	return b.String()
}

// An ARG's default is substituted where it is used.
func TestArgDefaultIsExpanded(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    ARG version=1.2.3
    RUN build --version=$version
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	if got := descriptions(p); !strings.Contains(got, "--version=1.2.3") {
		t.Errorf("the argument was not expanded:\n%s", got)
	}
}

// The braced form is the same thing, and is what people reach for when a
// variable is followed by a letter.
func TestBracedArgsExpand(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    ARG tag=v9
    RUN echo ${tag}-suffix
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	if got := descriptions(p); !strings.Contains(got, "v9-suffix") {
		t.Errorf("the braced argument was not expanded:\n%s", got)
	}
}

// A value supplied on the command line beats the default.
func TestSuppliedArgsOverrideDefaults(t *testing.T) {
	t.Parallel()

	src := versioned + "\nbuild:\n    FROM alpine\n    ARG version=default\n    RUN build $version\n"

	p, err := interp.Build(src, "build", interp.WithArgs(map[string]string{"version": "supplied"}))
	if err != nil {
		t.Fatal(err)
	}

	got := descriptions(p)
	if !strings.Contains(got, "build supplied") {
		t.Errorf("the supplied value was not used:\n%s", got)
	}
}

// Different argument values must produce different cache keys.
//
// An argument that changed what a step does without changing its key is a false
// hit - the same defect as an edited COPY source, arriving by a different route.
func TestArgValuesReachTheKey(t *testing.T) {
	t.Parallel()

	src := versioned + "\nbuild:\n    FROM alpine\n    ARG version=x\n    RUN build $version\n"

	key := func(v string) core.Key {
		p, err := interp.Build(src, "build", interp.WithArgs(map[string]string{"version": v}))
		if err != nil {
			t.Fatal(err)
		}

		return core.DeriveChainKey(p.Graph.Root, []ir.NodeID{{1}}, nil)
	}

	if key("one") == key("two") {
		t.Error("two argument values produced the same key; the build would hit the cache after a change")
	}

	// The same value, spelled twice, so what is compared is two interpretations
	// of the same Earthfile rather than one result against itself.
	first, second := "same", "sa"+"me"
	if key(first) != key(second) {
		t.Error("the same argument value produced different keys; nothing would ever hit")
	}
}

// An ARG that is declared and never mentioned still reaches the step, because a
// build argument *is* an environment variable there.
//
// **This asserted the opposite, and the opposite is not what the reference
// does.** The rationale was a good one - "otherwise adding an argument for one
// target invalidates every step in the file, and people learn not to add
// arguments" - and it described a property this engine had and earthly does not.
// Differentially, on an argument no command names:
//
//	ARG NEVER_MENTIONED=surprise
//	RUN env | grep NEVER_MENTIONED || echo NOT-IN-ENV
//
//	earthly   NEVER_MENTIONED=surprise
//	earth     NOT-IN-ENV
//
// So a declared argument changes the environment, the environment is part of
// Κ₁ (green paper 4.5), and the graph moves. The cost of the old property was
// not cache-friendliness: `+all-binaries` built five platforms, reported
// success, and wrote five identical linux/arm64 binaries - the darwin ones and
// the .exe included - because `go build` reads GOOS from an environment that
// never had it (E580).
func TestADeclaredArgReachesTheStepEvenWhenNothingNamesIt(t *testing.T) {
	t.Parallel()

	with, err := interp.Build(versioned+"\nbuild:\n    FROM alpine\n    ARG unused=1\n    RUN make\n", "build")
	if err != nil {
		t.Fatal(err)
	}

	without, err := interp.Build(versioned+"\nbuild:\n    FROM alpine\n    RUN make\n", "build")
	if err != nil {
		t.Fatal(err)
	}

	if with.Graph.Root.ID() == without.Graph.Root.ID() {
		t.Error("a declared argument did not reach the step:" +
			"\n  it is an environment variable there, so the two graphs must differ")
	}

	for _, n := range with.Graph.Nodes() {
		if strings.Contains(n.Meta.Description, "RUN make") && n.Op.Env["unused"] != "1" {
			t.Errorf("the argument is not in the step's environment: %v", n.Op.Env)
		}
	}
}

// A `$name` that is not a declared argument is left alone.
//
// It belongs to the shell - `for i in 1 2 3; do echo $i; done` is an ordinary
// RUN - and expanding it to the empty string would silently corrupt the command.
// Only what the Earthfile declares is ours to substitute.
func TestUndeclaredVariablesAreLeftForTheShell(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    RUN for i in 1 2 3; do echo $i; done
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	got := descriptions(p)
	if !strings.Contains(got, "$i") {
		t.Errorf("an undeclared variable was expanded away; it belonged to the shell:\n%s", got)
	}
}

// An ARG declared after the step that uses it is not in scope there. Silently
// treating it as empty would make the order of a file change its meaning
// invisibly.
func TestArgsApplyOnlyAfterTheyAreDeclared(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    RUN echo $late
    ARG late=too-late
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	if got := descriptions(p); !strings.Contains(got, "$late") {
		t.Errorf("an argument declared later was expanded earlier:\n%s", got)
	}
}
