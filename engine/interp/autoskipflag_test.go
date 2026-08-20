package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// Naming `--build-auto-skip` does not refuse a file that never uses it.
//
// The flag enables `BUILD --auto-skip` on individual commands - it is permission
// to write an option, not behaviour of its own. This engine refuses that option
// by name already, which is exactly the condition `ignoredFeatures` states for
// accepting a flag: the file is refused where the construct is used, and a file
// that only declares the dialect builds (E414).
//
// Eight targets in `tests/` were being refused at their VERSION line for an
// option they never wrote.
func TestNamingTheAutoSkipFlagDoesNotRefuseTheFile(t *testing.T) {
	t.Parallel()

	src := "VERSION --build-auto-skip 0.8\nmain:\n    FROM alpine\n    RUN true\n"

	if _, err := interp.Build(src, "main"); err != nil {
		t.Errorf("a file naming --build-auto-skip was refused although it uses no"+
			" --auto-skip: %v", err)
	}
}

// And the option itself is accepted, which is a decision that was made the
// other way first.
//
// What stood here refused it, on the grounds that accepting is "a silent claim
// to a feature - a build that skips nothing while saying it may". That is a real
// worry and it is the wrong one, because of what the flag asks for: `--auto-skip`
// does not change what a build *produces*, only how fast it gets there. Ignoring
// it costs time; refusing it costs a working build, and
// `tests/wildcard-build.earth` drives one expecting to build (E484).
//
// The engine already answers the same request under another name -
// `SAVE IMAGE --cache-hint` is accepted and ignored, with I5 written beside it -
// so refusing this one was two answers to one question, which is the shape E476
// found in `--allow-privileged`.
//
// The safe direction is the one that does the work: a skipped target with a side
// effect is a side effect that did not happen, and this engine not skipping can
// only ever be slower.
func TestTheAutoSkipOptionIsAccepted(t *testing.T) {
	t.Parallel()

	src := "VERSION --build-auto-skip 0.8\nsub:\n    FROM alpine\n    RUN true\n" +
		"main:\n    FROM alpine\n    BUILD --auto-skip +sub\n"

	p, err := interp.Build(src, "main")
	if err != nil {
		t.Fatalf("BUILD --auto-skip was refused: %v", err)
	}

	// The target is built rather than quietly dropped, which is what "not
	// skipping" has to mean.
	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "true") {
		t.Errorf("the target named by an --auto-skip BUILD is not in the"+
			" graph:\n%s", got)
	}
}
