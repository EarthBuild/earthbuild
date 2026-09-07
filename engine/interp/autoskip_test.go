package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `BUILD --auto-skip` asks for what this engine's cache already does.
//
// The flag skips a target whose inputs have not changed since a previous build.
// That is what a chain key is: a step whose inputs are identical is served from
// the cache and does not run, and the engine reaches the same answer without
// being asked (§4.4, I5).
//
// **I5 is what makes accepting it safe**: a cache hint may not change results,
// so a flag that only asks for a faster route to the same answer can be ignored
// without changing what a build produces. That is the reasoning already written
// beside `SAVE IMAGE --cache-hint` and `--cache-from`, and this is the same
// flag wearing a different name (E484).
//
// `tests/wildcard-build.earth` drives it expecting a build, not a refusal.
func TestAutoSkipIsAcceptedAndTheTargetIsStillBuilt(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    BUILD --auto-skip +dep\n"+
		"\ndep:\n    FROM alpine:3.22\n    RUN make thing\n", testMain)
	if err != nil {
		t.Fatalf("BUILD --auto-skip was refused: %v"+
			"\n  it asks for a faster route to the answer this engine already"+
			" gives", err)
	}

	// Accepted *and the target built*, which is the half that matters: a flag
	// that quietly dropped the BUILD would also "plan", and the difference is
	// a target nobody notices is missing.
	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "make thing") {
		t.Errorf("the referenced target is not in the graph:\n%s", got)
	}
}

// And it changes nothing about the plan.
//
// The flag is a hint, so the graph with it and the graph without it are the same
// graph. Asserted rather than assumed: an ignored flag that quietly altered the
// key would make every build before it a miss.
func TestAutoSkipDecidesNothingAboutThePlan(t *testing.T) {
	t.Parallel()

	const recipe = "\nmain:\n    FROM alpine:3.22\n    BUILD %s+dep\n" +
		"\ndep:\n    FROM alpine:3.22\n    RUN make thing\n"

	with := planID(t, versioned+strings.Replace(recipe, "%s", "--auto-skip ", 1))
	without := planID(t, versioned+strings.Replace(recipe, "%s", "", 1))

	if with != without {
		t.Errorf("the plan is %s with the flag and %s without it, so a hint"+
			" moved the key and every earlier build is now a miss", with, without)
	}
}

// planID fingerprints a plan by its root.
func planID(t *testing.T, src string) string {
	t.Helper()

	p, err := interp.Build(src, testMain)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	return p.Graph.Root.ID().String()
}
