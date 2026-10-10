package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// LET declares a variable; the commands after it see its value.
func TestLetDeclares(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    LET version=1.2.3
    RUN build --version=$version
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "--version=1.2.3") {
		t.Errorf("the variable was not substituted:\n%s", got)
	}
}

// SET changes it, for the commands after the SET and no others.
//
// A recipe read top to bottom means one thing: the step before the SET sees the
// old value. Applying it retroactively would make the order of a file change
// what it built without changing what it says.
func TestSetChangesLaterStepsOnly(t *testing.T) {
	t.Parallel()

	// The dialect that has SET, which a real Earthfile must also declare: the
	// construct is gated on the flag now, because a file using it without one
	// builds here and nowhere else (E458).
	p, err := interp.Build(setVersioned+`
build:
    FROM alpine:3.22
    LET stage=first
    RUN echo $stage
    SET stage=second
    RUN echo $stage
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	for _, want := range []string{"echo first", "echo second"} {
		if !strings.Contains(got, want) {
			t.Errorf("the graph is missing %q:\n%s", want, got)
		}
	}
}

// SET on something never declared is refused.
//
// That distinction is the whole reason the language has both: LET introduces,
// SET updates. Treating SET as a declaration would make a typo in a variable
// name silently create a second variable, and the original keeps its old value
// while the author believes it changed.
func TestSetRequiresADeclaration(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(setVersioned+`
build:
    FROM alpine:3.22
    SET never_declared=x
`, "build")
	if err == nil {
		t.Fatal("SET on an undeclared variable was accepted")
	}

	for _, want := range []string{"never_declared", testCmdLet} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, err)
		}
	}
}

// A value that must be computed by running something is refused, naming it.
//
// `LET x=$(cat file)` needs a filesystem and a shell. Guessing would produce a
// build that used a value nobody chose.
func TestShellOutValuesAreRefused(t *testing.T) {
	t.Parallel()

	// No runner is supplied, which is the plan-only path: producing a graph
	// must not run commands in a sandbox behind the caller's back.
	_, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    LET result=$(cat version.txt)
    RUN echo $result
`, "build")
	if err == nil {
		t.Fatal("a value requiring execution was accepted")
	}

	// The command itself, not the `$(` wrapper: the reader needs to know which
	// command the build wanted to run, and it is the one thing the refusal is
	// about.
	if !strings.Contains(err.Error(), "cat version.txt") {
		t.Errorf("the refusal does not name the command:\n%s", err)
	}
}

// A different value is a different build.
func TestLetValuesReachTheGraph(t *testing.T) {
	t.Parallel()

	mk := func(v string) string {
		p, err := interp.Build(versioned+"\nbuild:\n    FROM alpine\n    LET v="+v+"\n    RUN make $v\n", "build")
		if err != nil {
			t.Fatal(err)
		}

		return p.Graph.Root.ID().String()
	}

	if mk("one") == mk("two") {
		t.Error("two values produced the same step")
	}
}

// LET is not an ARG: it is the recipe's own variable and is not overridable
// from outside.
func TestLetIsNotOverridableFromOutside(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    LET v=internal
    RUN echo $v
`, "build", interp.WithArgs(map[string]string{"v": "external"}))
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "echo internal") {
		t.Errorf("an outside value overrode a LET:\n%s", got)
	}
}

// setVersioned is a VERSION line whose dialect has SET (E458).
const setVersioned = "VERSION --arg-scope-and-set 0.8\n"
