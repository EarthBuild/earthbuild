package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestANumericComparisonIsDecidedWithoutAProbe.
//
// **A probe is a container round trip**, and `IF [ "$level" -gt "0" ]` is how
// the corpus writes a bounded loop: `command.earth`'s `RECURSIVE` counts down
// from 5, so five conditions become five round trips before a single step of
// real work happens. That target now times out where it used to fail
// immediately - which is progress, and also the reason to fix this.
//
// The values are known: `level` is a build argument and `0` is a literal, so
// the answer is arithmetic and the engine can do arithmetic. `=` and `!=` were
// already decided here for exactly this reason; the numeric operators are the
// same argument in the same place.
//
// Not a shortcut around correctness: a comparison whose operands are *not* both
// numbers is still sent to the shell, because `[ x -gt 0 ]` is an error there
// and guessing at one here would be inventing a language.
func TestANumericComparisonIsDecidedWithoutAProbe(t *testing.T) {
	t.Parallel()

	r := &recorder{result: true}

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    ARG level=5
    IF [ "$level" -gt "0" ]
        RUN counted-down
    END
    IF [ "$level" -le "4" ]
        RUN should-not-run
    END
`, testMain, interp.WithCommands(r.run))
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())

	if !strings.Contains(got, "counted-down") {
		t.Errorf("5 -gt 0 did not take the branch:\n%s", got)
	}

	if strings.Contains(got, "should-not-run") {
		t.Errorf("5 -le 4 took the branch:\n%s", got)
	}

	if len(r.calls) != 0 {
		t.Errorf("%d probe(s) were run: %v"+
			"\n  both operands are known, so this is arithmetic and costs a"+
			" container round trip only because nobody did it here", len(r.calls), r.calls)
	}
}

// TestANonNumericComparisonStillGoesToTheShell.
//
// `[ x -gt 0 ]` is an *error* in a shell, not false. An engine that answered it
// would be inventing a language, so an operand that is not an integer is sent
// where the rules for it live - which is also what makes the fast path safe to
// take without looking at anything else.
func TestANonNumericComparisonStillGoesToTheShell(t *testing.T) {
	t.Parallel()

	r := &recorder{result: true}

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    ARG level=notanumber
    IF [ "$level" -gt "0" ]
        RUN whatever
    END
`, testMain, interp.WithCommands(r.run))
	if err != nil {
		t.Fatal(err)
	}

	if len(r.calls) == 0 {
		t.Error("a comparison this engine cannot answer was answered anyway;" +
			" the shell's rules for it are not ours to guess")
	}
}
