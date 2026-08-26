package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestAForOverNothingRunsNothing.
//
// `tests/for.earth+test-for-empty` is four loops that must not execute, and it
// says so the only way a build can: each body is `false`.
//
//	FOR variable IN ""
//	    RUN echo "fail! variable='$variable'"; false
//	END
//
// This engine ran the body once with an empty value, so the step failed - and
// the *reported* failure was three loops later, a probe at line 46 that could
// not run because the chain it stood on had already failed. The diagnostic
// named both, which is the only reason the real line was findable.
//
// An empty string is not an item. Neither is the whitespace between two of
// them, which is the same rule stated once.
func TestAForOverNothingRunsNothing(t *testing.T) {
	t.Parallel()

	// `""`, `''` and a pair of them. Deliberately *not* `"   "`: a quoted run
	// of spaces is one argument in a shell and would iterate once, and this
	// test is for what the corpus states rather than what looks similar.
	for _, list := range []string{`""`, `''`, `"" ""`} {
		p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    FOR v IN `+list+`
        RUN ran-the-body
    END
    RUN after
`, testMain)
		if err != nil {
			t.Fatalf("FOR v IN %s: %v", list, err)
		}

		if got := describe(p.Graph.Nodes()); strings.Contains(got, "ran-the-body") {
			t.Errorf("FOR v IN %s ran its body:\n%s", list, got)
		}
	}

	// And a list with something in it still iterates over that something.
	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    FOR v IN "a" "" "b"
        RUN saw-$v
    END
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	for _, want := range []string{"saw-a", "saw-b"} {
		if !strings.Contains(got, want) {
			t.Errorf("the loop did not run for %q:\n%s", want, got)
		}
	}

	if strings.Contains(got, "saw-\n") || strings.Contains(got, "saw- ") {
		t.Errorf("the loop ran for an empty item:\n%s", got)
	}
}
