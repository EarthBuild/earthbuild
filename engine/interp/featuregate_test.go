package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A construct is refused where the file did not ask for it.
//
// `features.go` gates `--try` and `--pass-args` and lists the rest as
// understood-and-ignored, on the honest grounds that accepting a flag is a
// statement about the dialect rather than a claim to the feature. That is the
// right rule for the flag. **It says nothing about the construct**, and the
// corpus has three targets that exist to prove the other half: a file that uses
// the construct *without* the flag must be refused, and this engine built all
// three (E458).
//
// The failure class is the one this engine is arranged against, in the direction
// that is easy to miss: an Earthfile written against this engine, using a
// construct it never opted into, builds here and fails for everybody else.
func TestSetNeedsTheFeatureThatEnablesIt(t *testing.T) {
	t.Parallel()

	// **0.7, not 0.8.** This asserted the refusal at 0.8, and it was wrong:
	// `features.ArgScopeSet` carries `enabled_in_version:"0.8"`, and the
	// reference refuses SET on that field alone (`handleSet`). E458 read
	// `tests/arg-set.earth` - a `--should_fail` file that is itself `VERSION
	// 0.8` - as evidence the flag was still needed, when what it proves is that
	// the file fails for some *other* reason.
	//
	// The cost of the mistake was not the one construct. `LET`/`SET` are how
	// the corpus computes anything, so every target using them was refused or
	// silently left a variable unset - `wildcard-copy.earth`'s whole test
	// function counts files with `SET count=$(...)` and counted zero.
	_, err := interp.Build("VERSION 0.7\n\nmain:\n    FROM alpine:3.22\n"+
		"    ARG foo\n    SET foo = bar\n    RUN echo $foo\n", testMain)
	if err == nil {
		t.Fatal("SET was accepted by a file too old to have it")
	}

	for _, want := range []string{"SET", "--arg-scope-and-set"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refused with %q, which does not mention %q", err, want)
		}
	}
}

// And 0.8 has it without asking, which is the half that was wrong.
//
// A feature `enabled_in_version:"0.8"` is part of the dialect from 0.8 on, and
// a file stops naming a flag once its version implies it. Gating on the flag
// alone refuses what every other engine builds.
func TestSetIsOrdinaryAtVersionEightPointZero(t *testing.T) {
	t.Parallel()

	// LET rather than ARG: an ARG is the target's interface and SET refuses it
	// (TestSetRefusesAnArgAndSaysHowToFixIt), so writing the example that way
	// would test the wrong rule.
	got := commandOfFirstExec(t, "VERSION 0.8\n\nmain:\n"+
		"    FROM alpine:3.22\n    LET foo = one\n    SET foo = two\n    RUN echo $foo\n")

	if !strings.HasSuffix(got, "echo two") {
		t.Errorf("the step runs %q; SET is ordinary at 0.8 and must update"+
			" the argument without a flag", got)
	}
}

// And it is accepted where the file did ask.
func TestSetIsFineWithTheFeature(t *testing.T) {
	t.Parallel()

	got := commandOfFirstExec(t, "VERSION --arg-scope-and-set 0.8\n\nmain:\n"+
		"    FROM alpine:3.22\n    LET foo = one\n    SET foo = two\n    RUN echo $foo\n")

	if !strings.HasSuffix(got, "echo two") {
		t.Errorf("the step runs %q, and SET updated the argument", got)
	}
}

// `COMMAND` is the old spelling, and a file that asked for the new one may not
// use it.
//
// `tests/function.earth` declares `VERSION 0.8` and has a target whose whole
// purpose is to be refused for writing `COMMAND` where the file's dialect has
// `FUNCTION`. Accepting both spellings everywhere is the tempting answer and is
// what makes an Earthfile stop being portable: it builds here and nowhere else.
func TestTheOldCommandKeywordNeedsTheOldDialect(t *testing.T) {
	t.Parallel()

	_, err := interp.Build("VERSION --use-function-keyword 0.8\n\n"+
		"MYFN:\n    COMMAND\n    RUN echo hi\n\n"+
		"main:\n    FROM alpine:3.22\n    DO +MYFN\n", testMain)
	if err == nil {
		t.Fatal("COMMAND was accepted by a file that asked for FUNCTION")
	}

	if !strings.Contains(err.Error(), "COMMAND") {
		t.Errorf("refused with %q, which does not name the keyword", err)
	}
}

// At 0.7 `COMMAND` is the spelling and stays legal.
func TestTheOldCommandKeywordIsFineInTheOldDialect(t *testing.T) {
	t.Parallel()

	_, err := interp.Build("VERSION 0.7\n\n"+
		"MYFN:\n    COMMAND\n    RUN echo hi\n\n"+
		"main:\n    FROM alpine:3.22\n    DO +MYFN\n", testMain)
	if err != nil {
		t.Fatalf("COMMAND was refused by a file that never asked for FUNCTION: %v", err)
	}
}

// Each dialect has one spelling, and refuses the other.
//
// The corpus says so in its own comments - `tests/command.earth` opens with
// *"Do not update this to 0.8 (function.earth is used for testing 0.8)"* - and
// carries the mirror of `function.earth`'s target: at 0.7, writing `FUNCTION`
// must fail (E459).
//
// So the rename is a **version default** rather than only a flag, exactly as
// `--pass-args` is: a file stops naming the flag once its version implies it,
// and an engine that gates on the flag alone accepts what the reference refuses.
func TestEachDialectRefusesTheOtherSpelling(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, version, keyword string }{
		{"0.8 has FUNCTION and not COMMAND", "VERSION 0.8", "COMMAND"},
		{"0.7 has COMMAND and not FUNCTION", "VERSION 0.7", "FUNCTION"},
	} {
		_, err := interp.Build(tc.version+"\n\n"+
			"MYFN:\n    "+tc.keyword+"\n    RUN echo hi\n\n"+
			"main:\n    FROM alpine:3.22\n    DO +MYFN\n", testMain)
		if err == nil {
			t.Errorf("%s: %s was accepted", tc.name, tc.keyword)

			continue
		}

		if !strings.Contains(err.Error(), tc.keyword) {
			t.Errorf("%s: refused with %q, which does not name the keyword",
				tc.name, err)
		}
	}
}

// And the spelling each dialect does have keeps working.
func TestEachDialectAcceptsItsOwnSpelling(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ version, keyword string }{
		{"VERSION 0.8", "FUNCTION"},
		{"VERSION 0.7", "COMMAND"},
	} {
		_, err := interp.Build(tc.version+"\n\n"+
			"MYFN:\n    "+tc.keyword+"\n    RUN echo hi\n\n"+
			"main:\n    FROM alpine:3.22\n    DO +MYFN\n", testMain)
		if err != nil {
			t.Errorf("%s with %s: %v", tc.version, tc.keyword, err)
		}
	}
}
