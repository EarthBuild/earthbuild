package interp_test

import (
	"strings"
	"testing"
)

// A value substituted inside double quotes cannot end them.
//
// `tests/build-arg.earth` writes a value that contains quotes and compares it:
//
//	RUN printf '"text with quotes"' >./content
//	ARG VAR1=$(cat ./content)
//	RUN test "$VAR1" == '"text with quotes"'
//
// Spliced raw, the command reaches the shell as `test ""text with quotes"" ==
// ...` - the value's own quotes close the author's, the word splits into three,
// and the shell answers `sh: with: unknown operand` (E450).
//
// The substitution is inside double quotes, so what the shell must see there is
// the value's characters, escaped for that context. This is what passing the
// argument as environment would have done - the shell would expand it inside the
// quotes and no character of it would be syntax.
func TestAValueWithQuotesSurvivesSubstitution(t *testing.T) {
	t.Parallel()

	got := commandOfFirstExec(t, versioned+
		"\nmain:\n    FROM alpine:3.22\n"+
		"    ARG V=\"a \\\"quoted\\\" b\"\n"+
		"    RUN test \"$V\" = x\n")

	// The shell must see one argument. Whatever the escaping looks like, the
	// author's quotes must still be the outermost ones.
	if strings.Contains(got, `test "a "quoted" b"`) {
		t.Errorf("the step runs %q, where the value's quotes closed the"+
			" author's and the word split", got)
	}

	if !strings.Contains(got, `\"quoted\"`) {
		t.Errorf("the step runs %q, and the value's quotes are not escaped for"+
			" the context they landed in", got)
	}
}

// Inside single quotes a shell expands nothing, and neither does this.
//
// `RUN echo '$V'` prints `$V` in every shell there is. This engine substituted
// regardless of context, so it printed the value - an Earthfile that means the
// literal text got something else, silently (E450).
func TestSingleQuotesAreNotExpanded(t *testing.T) {
	t.Parallel()

	got := commandOfFirstExec(t, versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG V=secret\n    RUN echo '$V'\n")

	if strings.Contains(got, "secret") {
		t.Errorf("the step runs %q; inside single quotes a shell expands"+
			" nothing", got)
	}
}

// Outside quotes, nothing is escaped.
//
// A bare `$V` is subject to the shell's word splitting, which is what an
// Earthfile writing `RUN cmd $FLAGS` depends on. Escaping there would turn a
// list of flags into one argument with spaces in it.
func TestOutsideQuotesTheValueIsSplicedAsWritten(t *testing.T) {
	t.Parallel()

	got := commandOfFirstExec(t, versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG FLAGS=-a -b\n    RUN ls $FLAGS\n")

	if !strings.HasSuffix(got, "ls -a -b") {
		t.Errorf("the step runs %q, and a bare expansion is the shell's to"+
			" split", got)
	}
}

// A dollar that survived expansion is left for the step's shell.
//
// This engine's rule, written where expansion is defined: `ARG WHERE=$HOME/x` is
// the author asking for the shell's HOME. Escaping `$` inside the author's
// quotes would take that back at the point of use, so the escaping stops at the
// characters that would end the string (E450).
//
// Asserted beside the escaping, because the two are one decision: what is
// escaped is what would break the *author's* syntax, and what is not is what the
// author's own rule says belongs to the shell.
func TestADollarThatSurvivedIsLeftForTheShell(t *testing.T) {
	t.Parallel()

	got := commandOfFirstExec(t, versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG WHERE=$HOME/somewhere\n"+
		"    RUN echo \"at $WHERE\"\n")

	if !strings.Contains(got, "$HOME/somewhere") {
		t.Errorf("the step runs %q, and an undeclared name is the shell's", got)
	}
}
