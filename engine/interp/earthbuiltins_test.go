package interp_test

import (
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

// A git builtin is still not answered.
//
// It needs a repository read this engine does not do, and an empty string is a
// claim to have looked: a step could not tell "not a repository" from "the
// engine forgot". Declared and unset expands to nothing, which is the same
// result with none of the claim.
func TestAGitBuiltinIsNotAnswered(t *testing.T) {
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

	for _, n := range plan.Graph.Nodes() {
		for _, a := range n.Op.Args {
			if strings.Contains(a, "[") && !strings.Contains(a, "[]") {
				t.Errorf("a git builtin was answered: %q", a)
			}
		}
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
