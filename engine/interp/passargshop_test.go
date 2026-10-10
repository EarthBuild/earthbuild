package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// An argument handed to a function that never declares it still has to reach the
// next `--pass-args`.
//
// **Two hops, and the middle one is the point.** `OUTER` is given `target` and
// declares only `extra`, so `target` is used by nothing there - and this engine
// forwarded what was *declared*, which dropped it. The reference forwards what
// was *supplied*, so `INNER` sees the caller's value and not its own default
// (E867, reproduced in E896a):
//
//	native    INNER target=+default
//	buildkit  INNER target=+mytarget
//
// The one-hop case works either way, because a caller that declares what it
// forwards has the same set both ways - which is how a test written from the
// mechanism rather than from this reproducer concluded there was no defect.
func TestAnArgumentSuppliedButNotDeclaredStillPasses(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
INNER:
    FUNCTION
    ARG target=+default
    ARG extra
    RUN inner --target=$target --extra=$extra
OUTER:
    FUNCTION
    ARG extra
    DO --pass-args +INNER --extra="prefixed $extra"
probe:
    FROM alpine:3.22
    DO --pass-args +OUTER --target=+mytarget --extra=given
`, "probe")
	if err != nil {
		t.Fatal(err)
	}

	got := descriptions(p)
	if !strings.Contains(got, "--target=+mytarget") {
		t.Errorf("`target` was supplied to OUTER and did not reach INNER:\n%s", got)
	}

	// The explicitly named one is the control: it survives either way, so a run
	// where it is missing says the reproducer itself broke rather than the
	// behaviour under test.
	if !strings.Contains(got, "--extra=prefixed given") {
		t.Errorf("the named argument did not arrive either, so this is not testing what it says:\n%s", got)
	}
}
