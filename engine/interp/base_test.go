package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

const withBase = `VERSION 0.8
FROM alpine:3.22
WORKDIR /app

deps:
    RUN apk add make

build:
    FROM +deps
    RUN make
`

// Commands before the first target are the *base recipe*, and every target
// starts from it.
//
// Ignoring it made every target in such a file look like it had no base image,
// which is a hundred of the refusals in this repository's own Earthfiles. It is
// also invisible in an author's own examples, because an author writing tests
// for their engine writes targets that begin with FROM.
func TestTargetsInheritTheBaseRecipe(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(withBase, "deps")
	if err != nil {
		t.Fatal(err)
	}

	kinds := make([]string, 0, len(p.Graph.Nodes()))

	for _, n := range p.Graph.Nodes() {
		kinds = append(kinds, n.Op.Kind.String())
	}

	if len(p.Graph.Nodes()) < 2 {
		t.Fatalf("the base recipe was not inherited; graph is %v", kinds)
	}

	if got := p.Graph.Nodes()[0].Op.Kind; got != ir.OpImage {
		t.Errorf("the first step is %v, want the base recipe's FROM", got)
	}
}

// WORKDIR sets where later commands run.
func TestWorkdirAppliesToLaterSteps(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(withBase, "deps")
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpExec {
			continue
		}

		if n.Op.Dir != testWorkdir {
			t.Errorf("%s runs in %q, want /app", n.Meta.Description, n.Op.Dir)
		}
	}
}

// A later WORKDIR replaces an earlier one, and a relative one is resolved
// against it - which is what every shell does and what an author expects.
func TestWorkdirsCompose(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(`VERSION 0.8

build:
    FROM alpine
    WORKDIR /app
    WORKDIR src
    RUN make
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec && n.Op.Dir != "/app/src" {
			t.Errorf("%s runs in %q, want /app/src", n.Meta.Description, n.Op.Dir)
		}
	}
}

// The working directory changes what a command does, so it changes the step.
func TestWorkdirReachesTheGraph(t *testing.T) {
	t.Parallel()

	mk := func(dir string) ir.NodeID {
		p, err := interp.Build("VERSION 0.8\n\nbuild:\n    FROM alpine\n    WORKDIR "+dir+"\n    RUN make\n", "build")
		if err != nil {
			t.Fatal(err)
		}

		return p.Graph.Root.ID()
	}

	if mk("/one") == mk("/two") {
		t.Error("the same command in two directories produced one step")
	}
}

// The base recipe is shared, so two targets that inherit it inherit the *same*
// steps rather than two copies.
func TestTheBaseRecipeIsSharedBetweenTargets(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(withBase, "build")
	if err != nil {
		t.Fatal(err)
	}

	var images int

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpImage {
			images++
		}
	}

	if images != 1 {
		t.Errorf("the base image appears %d times, want 1:\n%s", images, describe(p.Graph.Nodes()))
	}
}

// A target that begins with its own FROM replaces the base recipe rather than
// stacking on it.
func TestAnExplicitFromReplacesTheBase(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(`VERSION 0.8
FROM alpine:3.22

build:
    FROM ubuntu:24.04
    RUN make
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	if strings.Contains(got, "alpine") {
		t.Errorf("an explicit FROM did not replace the base recipe:\n%s", got)
	}
}

// `+base` names the base recipe: the commands before the first target.
//
// It is a reserved name - the parser refuses a target called `base` - so a
// reference to it can only mean the implicit one. Looking only in the named
// targets reported "no target named base" 137 times across this repository,
// which is true of the list it searched and useless to the reader.
func TestBaseReferencesTheBaseRecipe(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
FROM alpine:3.22
RUN shared-setup

build:
    FROM +base
    RUN build-step
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	for _, want := range []string{"shared-setup", "build-step"} {
		if !strings.Contains(got, want) {
			t.Errorf("the graph is missing %q:\n%s", want, got)
		}
	}
}

// And across files, which is how a test directory reuses the repository's base.
func TestBaseAcrossFiles(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{
		testEarthfile: versioned + "\nFROM alpine:3.22\nRUN root-setup\n\nplaceholder:\n    RUN x\n",
		"tests/Earthfile": versioned + `
run:
    FROM ..+base
    RUN test-step
`,
	})

	p, err := buildIn(t, root+"/tests", "run")
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "root-setup") {
		t.Errorf("the parent's base recipe was not used:\n%s", got)
	}
}
