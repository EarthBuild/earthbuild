package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestACommandKeepsItsPipelineWhenAnArgumentSpansLines.
//
// **A listing is a multi-line value, and counting one is the point of having
// it.** `LET files=$(ls -d helloworld*)` then `LET n=$(echo "$files"|wc -l)` is
// how the corpus counts files; with three files in hand, `n` came back as the
// three filenames rather than 3, so the `wc -l` never ran. Single-line values
// count correctly, which is what points at the newline rather than the pipe.
//
// The command is what this asserts, not the answer: a probe's answer comes from
// a container, and the fault is that the string handed to it stops at the first
// newline of a substituted argument.
func TestACommandKeepsItsPipelineWhenAnArgumentSpansLines(t *testing.T) {
	t.Parallel()

	r := &recorder{result: true, output: "one\ntwo\nthree"}

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    LET files=$(ls)
    LET n=$(echo "$files"|wc -l)
    RUN echo $n
`, testMain, interp.WithCommands(r.run))
	if err != nil {
		t.Fatal(err)
	}

	var counted bool

	for _, c := range r.calls {
		if strings.Contains(strings.Join(c, " "), "wc -l") {
			counted = true
		}
	}

	if !counted {
		t.Errorf("no probe was asked to count lines; the commands run were %q"+
			"\n  the pipeline is lost at the first newline of the value"+
			" substituted into it, so the count is the listing itself", r.calls)
	}

	// **And the value must already be in it.** `$files` is an engine variable,
	// not an environment one, so a container handed `echo "$files"|wc -l`
	// verbatim expands it to nothing and counts one empty line. Asserting only
	// that `wc -l` appears cannot tell the two apart - both spellings contain
	// it - and the difference is the whole question.
	for _, c := range r.calls {
		joined := strings.Join(c, " ")
		if !strings.Contains(joined, "wc -l") {
			continue
		}

		if strings.Contains(joined, "$files") {
			t.Errorf("the probe was sent %q with the name unexpanded", joined)
		}

		if !strings.Contains(joined, "two") {
			t.Errorf("the probe was sent %q, which does not carry the value's"+
				" second line - the command stops at the first newline", joined)
		}
	}
}

// TestASubstitutionKeepsItsQuotingEvenInsideAValue.
//
// The rule at the top of `command` is right and was applied to the wrong unit:
// *"a command line is re-parsed by a shell, so its quoting is preserved;
// everything else is a value this engine consumes, so its quoting is
// resolved."* A `$(...)` inside a value is a command line - it is handed to a
// shell - so the distinction is between *regions* of an argument, not between
// commands.
//
// Resolved wholesale, `LET n=$(echo "$files" | wc -l)` reached the probe as
// `echo one\ntwo\nthree | wc -l`: the quotes that held a three-line value
// together were gone, so the shell ran `echo one`, then `two` and `three` as
// commands of their own, and the pipeline counted whatever survived. The
// symptom is worse than a wrong number - lines of a value become commands.
func TestASubstitutionKeepsItsQuotingEvenInsideAValue(t *testing.T) {
	t.Parallel()

	r := &recorder{result: true, output: "x"}

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    LET n=$(echo "a  b" | tr -d z)
    RUN echo $n
`, testMain, interp.WithCommands(r.run))
	if err != nil {
		t.Fatal(err)
	}

	if len(r.calls) == 0 {
		t.Fatal("the substitution was never run")
	}

	got := strings.Join(r.calls[0], " ")
	if !strings.Contains(got, `"a  b"`) {
		t.Errorf("the probe was sent %q"+
			"\n  the quotes are the shell's, not this engine's, and without them"+
			" the words inside them are separate arguments", got)
	}
}
