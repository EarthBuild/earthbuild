package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestAPushStepRunsOnlyInPushMode.
//
// `RUN --push` says *this step belongs to a push* - publishing a release,
// tagging a registry, posting a notification. Planning it away is right for an
// ordinary build, and it is what this engine did unconditionally: there was no
// push mode to be in, so `tests/push.earth` ran nothing and asserted nothing.
//
// The flag is the caller's statement that this build is a push. Nothing about
// the step is special once it is: it is a RUN, and it runs.
//
// Deliberately not the same question as pushing an *image* to a registry.
// `SAVE IMAGE --push` needs a registry, credentials and a network; a
// `RUN --push` needs a shell. Conflating them is why this was left undone.
func TestAPushStepRunsOnlyInPushMode(t *testing.T) {
	t.Parallel()

	const src = `
main:
    FROM alpine:3.22
    RUN --push publish-the-thing
    RUN ordinary-step
`

	// Without it: planned away, and the build is otherwise whole.
	p, err := interp.Build(versioned+src, testMain)
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	if strings.Contains(got, "publish-the-thing") {
		t.Error("a push step ran in an ordinary build, which is what the flag" +
			" exists to prevent")
	}

	if !strings.Contains(got, "ordinary-step") {
		t.Errorf("the rest of the recipe went with it:\n%s", got)
	}

	// With it: run, in place.
	p, err = interp.Build(versioned+src, testMain, interp.WithPush(true))
	if err != nil {
		t.Fatal(err)
	}

	got = describe(p.Graph.Nodes())
	if !strings.Contains(got, "publish-the-thing") {
		t.Errorf("the caller said this build is a push and the step was still"+
			" dropped:\n%s", got)
	}

	// And in the order it was written: a push step is part of the recipe, not
	// an appendix to it.
	if strings.Index(got, "publish-the-thing") > strings.Index(got, "ordinary-step") {
		t.Errorf("the push step was reordered:\n%s", got)
	}
}
