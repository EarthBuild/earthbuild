package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A flag given a value it cannot take says so in this engine's words.
//
// `tests/true-false-flag-invalid.earth` writes `RUN --no-cache=maybe` on
// purpose, and refusing it is right. What came back was the flag library's:
//
//	invalid argument for flag `--no-cache' (expected bool): strconv.ParseBool: parsing "maybe": invalid syntax
//
// which names a Go function, a Go package's idea of syntax, and quotes the flag
// with a backtick and an apostrophe. Every other refusal in this engine says
// what failed, where, what was expected and what to write instead - and a
// diagnostic that reads like a stack trace sends the reader to the wrong
// language (E451).
func TestAFlagWithAValueItCannotTakeSaysSo(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN --no-cache=maybe echo hi\n", testMain)
	if err == nil {
		t.Fatal("`--no-cache=maybe` planned, and maybe is not a boolean")
	}

	for _, want := range []string{"--no-cache", "maybe", "true", "false", "Earthfile:5"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refused with %q, which does not mention %q", err, want)
		}
	}

	if strings.Contains(err.Error(), "strconv") {
		t.Errorf("refused with %q, which names a Go function at the reader", err)
	}
}

// A flag nobody has heard of still says which one.
//
// The same treatment, and the case that must not regress into a generic
// message: the reader's mistake is usually a typo, and the only useful thing a
// refusal can do is repeat what they typed.
func TestAnUnknownFlagIsNamed(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN --no-cash echo hi\n", testMain)
	if err == nil {
		t.Fatal("`--no-cash` planned")
	}

	if !strings.Contains(err.Error(), "--no-cash") {
		t.Errorf("refused with %q, which does not name the flag", err)
	}
}
