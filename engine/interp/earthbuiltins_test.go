package interp_test

import (
	"os"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The `EARTH_*` builtins a target can declare.
//
// `ARG TARGETARCH` worked and `ARG EARTH_TARGET_NAME` did not: the mechanism
// that supplies builtins on declaration covered the platform family and nothing
// else, so `tests/empty-git.earth` failed at execution on
// `test "" == "+test-empty"` (E423).
//
// **On declaration, like the platform ones.** An undeclared `$EARTH_TARGET_NAME`
// expands to nothing in the reference, and an engine that filled it in would
// change what an Earthfile means - the rule the platform builtins are already
// written to, quoted in their own comment.
func TestTheEarthBuiltinsATargetCanDeclare(t *testing.T) {
	t.Parallel()

	argv := func(t *testing.T, src, target string) string {
		t.Helper()

		plan, err := interp.Build(src, target)
		if err != nil {
			t.Fatalf("%v", err)
		}

		var out []string

		for _, n := range plan.Graph.Nodes() {
			if (n.Op.Kind == ir.OpExec || n.Op.Kind == ir.OpHost) && len(n.Op.Args) > 0 {
				out = n.Op.Args
			}
		}

		return strings.Join(out, " ")
	}

	got := argv(t, `
VERSION 0.8
build:
    FROM alpine
    ARG EARTH_TARGET_NAME
    ARG EARTH_TARGET
    ARG EARTH_LOCALLY
    RUN echo "[$EARTH_TARGET_NAME][$EARTH_TARGET][$EARTH_LOCALLY]"
`, "build")

	if want := "[build][+build][false]"; !strings.Contains(got, want) {
		t.Errorf("the declared builtins expanded to %s, want %s", got, want)
	}

	// A LOCALLY target says so, because a recipe that behaves differently on the
	// host is the reason the variable exists.
	local := argv(t, `
VERSION 0.8
build:
    LOCALLY
    ARG EARTH_LOCALLY
    RUN echo "[$EARTH_LOCALLY]"
`, "build")

	if !strings.Contains(local, "[true]") {
		t.Errorf("EARTH_LOCALLY is not true in a LOCALLY target: %s", local)
	}
}

// Undeclared, they expand to nothing - which is the reference's behaviour and
// the rule the platform builtins already follow.
func TestAnUndeclaredEarthBuiltinIsNotSupplied(t *testing.T) {
	t.Parallel()

	plan, err := interp.Build(`
VERSION 0.8
build:
    FROM alpine
    RUN echo "[$EARTH_TARGET_NAME]"
`, "build")
	if err != nil {
		t.Fatalf("%v", err)
	}

	for _, n := range plan.Graph.Nodes() {
		for _, a := range n.Op.Args {
			if strings.Contains(a, "[build]") {
				t.Errorf("an undeclared builtin was filled in: %q", a)
			}
		}
	}
}

// A declared git builtin is answered from the build context's repository.
//
// **This test used to assert the opposite, and the reason it gave was right at
// the time**: "it needs a repository read this engine does not do, and an empty
// string is a claim to have looked - a step could not tell 'not a repository'
// from 'the engine forgot'". The engine now does the read, so the premise is
// gone: an empty value means there is no repository, which is what the
// documentation promises and what a step can act on.
//
// The symptom of not answering was a binary. `earth +earthly` stamped itself
// `Version=dev-` and `GitSha=`, forty bytes smaller than the same target built
// by the reference engine and otherwise identical - provenance missing,
// reported as success (E563).
func TestADeclaredGitBuiltinIsAnswered(t *testing.T) {
	t.Parallel()

	plan, err := interp.Build(`
VERSION 0.8
build:
    FROM alpine
    ARG EARTH_GIT_HASH
    RUN echo "[$EARTH_GIT_HASH]"
`, "build")
	if err != nil {
		t.Fatalf("%v", err)
	}

	// **Unless there is no checkout.** `+unit-test` builds against a tree the
	// Earthfile assembled and `.git` is excluded from every build context, so
	// the builtins this asserts on have nothing to read and expand to nothing -
	// correctly. Failing there says the mechanism is broken when the repository
	// it needs is what is missing (E605).
	_, err = os.Stat("../../.git")
	if err != nil {
		t.Skip("no .git here: a build context never carries one, so the git" +
			" builtins have nothing to answer from")
	}

	// This test runs inside this repository's own checkout, so there is a
	// commit to report. A hash is forty hex characters and nothing else is, so
	// the shape is the assertion rather than any particular value - which would
	// be a test that fails on every commit.
	answered := false

	for _, n := range plan.Graph.Nodes() {
		for _, a := range n.Op.Args {
			_, after, opened := strings.Cut(a, "[")
			if !opened {
				continue
			}

			inside, _, closed := strings.Cut(after, "]")
			if !closed {
				continue
			}

			if len(inside) == 40 && strings.Trim(inside, "0123456789abcdef") == "" {
				answered = true
			}
		}
	}

	if !answered {
		t.Error("a declared git builtin expanded to nothing inside a checkout:" +
			"\n  an Earthfile that stamps a version with it ships an unstamped" +
			"\n  binary, and reports success")
	}
}

// A target named with its `+` gives the same builtins as one without.
//
// Both spellings reach the interpreter - `earth +build` writes the plus and
// `interp.Build(src, "build")` does not - and the first produced
// `EARTH_TARGET=++build` and an `EARTH_TARGET_NAME` carrying a plus that no
// comparison in any Earthfile expects. Found by running `tests/empty-git.earth`,
// which asserts on both names (E423).
func TestATargetNamedWithItsPlusGivesTheSameBuiltins(t *testing.T) {
	t.Parallel()

	src := `
VERSION 0.8
build:
    FROM alpine
    ARG EARTH_TARGET_NAME
    ARG EARTH_TARGET
    RUN echo "[$EARTH_TARGET_NAME][$EARTH_TARGET]"
`

	for _, name := range []string{"build", "+build"} {
		plan, err := interp.Build(src, name)
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}

		var got string

		for _, n := range plan.Graph.Nodes() {
			if n.Op.Kind == ir.OpExec && len(n.Op.Args) > 0 {
				got = strings.Join(n.Op.Args, " ")
			}
		}

		if want := "[build][+build]"; !strings.Contains(got, want) {
			t.Errorf("built as %q, the builtins expanded to %s, want %s", name, got, want)
		}
	}
}
