package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A second ARG does not overwrite a value the build already has.
//
// `ARG` declares a name and a *default*; it does not assign. So a base recipe
// that writes
//
//	ARG --global FOO = bar
//	ARG FOO = bacon
//
// leaves FOO as `bar`, and `tests/arg-redeclare-error.earth` asserts exactly
// that with `RUN test "$FOO" = "bar"`. This engine wrote `bacon`, which is the
// rule inverted: the *last* declaration won instead of the first value
// (E438).
//
// Found by the execution gate rather than by the planning sweep, because both
// spellings plan: the difference is only in what the step is handed.
func TestASecondArgDoesNotOverwriteAValue(t *testing.T) {
	t.Parallel()

	// Two declarations in one recipe and one scope are now an *error* rather
	// than a silently-kept first value: the corpus has two targets that exist to
	// be refused for exactly that, and the run gate caught this engine building
	// them (E456). That half of this test moved to
	// `TestDeclaringAnArgumentTwiceIsAnError`.
	//
	// What stays here is the half still true, and the one the corpus opens with:
	// **which value stands when two different scopes declare one name.**
	got := commandOfFirstExec(t, `VERSION 0.8

FROM alpine:3.22
ARG --global FOO = bar
ARG FOO = bacon

main:
    RUN echo $FOO
`)

	if !strings.HasSuffix(got, "echo bar") {
		t.Errorf("the step runs %q; tests/arg-redeclare-error.earth asserts bar", got)
	}
}

// A base-recipe ARG that is not `--global` stays in the base recipe.
//
// `tests/build-arg-explicit-global.earth` declares `ARG local=ghi` before the
// first target and asserts `test "$local" == ""` inside one - which is what
// `--global` is *for*: without it, the name is the base recipe's own. This
// engine passed it to every target, so the flag decided nothing (E438).
func TestANonGlobalBaseArgDoesNotReachATarget(t *testing.T) {
	t.Parallel()

	got := commandOfFirstExec(t, `VERSION 0.8

FROM alpine:3.22
ARG --global shared=abc
ARG mine=ghi

main:
    RUN echo [$shared] [$mine]
`)

	if !strings.Contains(got, "[abc]") {
		t.Errorf("the step runs %q, and a --global argument should be abc there", got)
	}

	// Not expanded to `ghi`. Whether it survives as `$mine` or expands to
	// nothing is a separate rule this engine already has: an undeclared name is
	// left for the shell, which is what `ARG WHERE=$HOME/x` depends on - and the
	// shell has no `mine` either, so the step sees an empty string. What must
	// not happen is the base recipe's value arriving.
	if strings.Contains(got, "ghi") {
		t.Errorf("the step runs %q; a base-recipe argument without --global"+
			" reached a target, so --global is a flag that decides nothing", got)
	}
}

// commandOfFirstExec plans a source and returns the command its first step runs.
//
// Arguments are expanded where they are written rather than passed as
// environment, so the expanded command *is* the observable: `RUN echo $FOO`
// plans as `echo bar` or `echo bacon`, and which one says whose value won.
func commandOfFirstExec(t *testing.T, src string) string {
	t.Helper()

	p, err := interp.Build(src, "main")
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec {
			return strings.Join(n.Op.Args, " ")
		}
	}

	return ""
}
