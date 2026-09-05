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

// A dollar that survived expansion is left as text, escaped.
//
// It used to be left live, on this engine's own rule that `ARG WHERE=$HOME/x` is
// the author asking for the step shell's HOME (E450). The reference has no such
// rule and cannot have one: the value goes to the step as environment, quoted,
// and a shell does not re-scan what an expansion produced. So the dollar
// survives as a character rather than as syntax (E964).
//
// Asserted beside the escaping, because the two are one decision: what reaches
// the step is the value the Earthfile computed, and nothing the step's shell
// does to it afterwards.
func TestADollarThatSurvivedIsLeftForTheShell(t *testing.T) {
	t.Parallel()

	got := commandOfFirstExec(t, versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG WHERE=$HOME/somewhere\n"+
		"    RUN echo \"at $WHERE\"\n")

	if !strings.Contains(got, `\$HOME/somewhere`) {
		t.Errorf("the step runs %q, and the value's own dollar is not syntax", got)
	}
}

// A substituted value is not re-read as syntax by the step's shell.
//
// The reference never splices at all: build arguments reach the step as
// environment, shell-escaped, and the command text is handed to an inner shell
// unexpanded (`earthfile2llb/shell.go`, strWithEnvVarsAndDocker). A shell does
// not re-scan the result of an expansion, so no character of the value is
// syntax. Splicing the value in reproduces that only if the splice escapes what
// the shell would otherwise act on - and `$` was not escaped, so
// `ARG VAR="literal\$(string)"` ran `string` and compared against its output
// (tests/shell-out/new.earth +test4, E964).
func TestASubstitutedValueIsNotReParsed(t *testing.T) {
	t.Parallel()

	got := commandOfFirstExec(t, versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG VAR1=\"literal\\$(string)\"\n"+
		"    RUN test \"$VAR1\" == \"literal\\$(string)\"\n")

	if strings.Contains(got, `"literal$(string)"`) {
		t.Errorf("the step runs %q, where the value's $( is the shell's to"+
			" execute", got)
	}
}

// The same value outside the author's quotes, where the shell would read a
// parenthesis as syntax rather than run a subshell's worth of it.
//
// Escaping stops short of what an unquoted expansion legitimately does - it
// still splits on whitespace and still globs - because the reference's inner
// shell does both to an expanded value. Only what would end or re-open the word
// is escaped.
func TestASubstitutedValueOutsideQuotesIsNotReParsed(t *testing.T) {
	t.Parallel()

	got := commandOfFirstExec(t, versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG VAR1=\"literal\\$(string)\"\n"+
		"    RUN echo -n $VAR1\n")

	if strings.Contains(got, "literal$(string)") {
		t.Errorf("the step runs %q, where $( and the parentheses are syntax", got)
	}
}

// Exec form is not re-parsed, so nothing in it is escaped.
//
// `RUN ["echo", "$VAR"]` hands the kernel an argv: there is no shell between the
// plan and the process, so a character of the value that would be syntax to one
// is simply a character. Escaping it there puts a backslash into the argument
// the program receives (E964).
func TestExecFormIsNotEscaped(t *testing.T) {
	t.Parallel()

	got := commandOfFirstExec(t, versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG VAR1=\"a\\$b\"\n"+
		"    RUN [\"echo\", \"$VAR1\"]\n")

	if strings.Contains(got, `\$b`) {
		t.Errorf("the step runs %q, and an argv element reaches no shell", got)
	}
}
