package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestAMultiLineValueIsStillAValueInACondition.
//
// **The corpus counts things by listing them**, and a listing has one line per
// item: `LET files=$(ls -d helloworld* || echo -n "")` then
// `IF [ "$files" != "" ]` is `wildcard-copy.earth`'s test function, and twelve
// targets go through it.
//
// Written while chasing those twelve, and it passed on the first run - which is
// the finding, not a wasted test. A newline inside a token is exactly the sort
// of thing to break a comparison, and ruling the decision layer out is what
// left the probe's *input* as the only remaining suspect: the value really is
// empty by the time the condition sees it, because the `LET` that computed it
// could not see the files a preceding artifact `COPY` had placed.
//
// So it stays, as the regression guard for the half that works: the decision is
// made here rather than by a probe - a condition sent to a probe would have been
// decided by the recorder's own answer and told us nothing.
func TestAMultiLineValueIsStillAValueInACondition(t *testing.T) {
	t.Parallel()

	// The probe answers the LET, which must succeed for the value to exist at
	// all. A condition reaching the probe would therefore be answered *true*
	// and the branch taken for the wrong reason - which the check on the
	// recorded calls below is there to catch.
	r := &recorder{result: true, output: "one\ntwo\nthree\n"}

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    LET files=$(ls)
    LET count=0
    IF [ "$files" != "" ]
        SET count=7
    END
    RUN echo $count
`, testMain, interp.WithCommands(r.run))
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	if !strings.Contains(got, "echo 7") {
		t.Errorf("a three-line value was not different from the empty string,"+
			" so the branch was not taken:\n%s", got)
	}

	for _, c := range r.calls {
		if strings.Contains(strings.Join(c, " "), "!=") {
			t.Errorf("the condition went to a probe: %v"+
				"\n  a comparison against a literal is decidable here, and"+
				" sending it away costs a step per conditional", c)
		}
	}
}
