package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// An escaped `\$(...)` in an ARG default is text, not a command.
//
// `ARG a=literal\$(echo run)` means the eleven characters `$(echo run)`. The
// backslash is the author saying so, and it is the only way to write a literal
// `$(` in a default at all.
//
// This engine ran it. Measured against earthly, which produces
// `literal$(echo run)` where this produced `literalrun` - the command the
// author escaped, executed.
//
// The escape was lost rather than ignored, which is why it took a differential
// to see. `commandSpan` is escape-aware and correctly left `\$(` in the text
// region; the text region is then unquoted, which turns `\$` into `$`; and the
// lazy command scan further down re-reads the unquoted text, where nothing
// distinguishes an escape that was honoured from a command that was written.
// Both passes are individually right and the pair loses the fact.
//
// Not a privilege question - an Earthfile that can write ARG can write RUN -
// but a build that executes what the author escaped is wrong twice: it runs
// something nobody asked to run, and it cannot express the literal.
func TestAnEscapedCommandInAnArgDefaultIsNotRun(t *testing.T) {
	t.Parallel()

	// No runner is configured, so a default treated as a command cannot be
	// evaluated and the build fails saying so. Succeeding is the assertion:
	// text needs no runner.
	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG a=literal\\$(echo run)\n"+
		"    RUN echo $a\n", testMain)
	if err != nil {
		t.Fatalf("an escaped command was treated as a command to run: %v", err)
	}
}

// The unescaped form is still a command, or the fix has traded one bug for its
// mirror image: a build that never runs a dynamic default is as wrong as one
// that runs an escaped one, and quieter.
func TestAnUnescapedCommandInAnArgDefaultIsStillRun(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG a=$(echo run)\n"+
		"    RUN echo $a\n", testMain)
	if err == nil {
		t.Fatal("a dynamic default planned with no way to evaluate it")
	}

	if !strings.Contains(err.Error(), "$(") && !strings.Contains(err.Error(), "echo run") {
		t.Errorf("refused with %q, which does not name the expression", err)
	}
}
