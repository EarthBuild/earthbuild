package subcmd

import (
	"os"
	"strings"
	"testing"
)

// Both spellings of a build argument reach the native engine.
//
// `--build-arg NAME=VALUE` and a trailing `+target --NAME=VALUE` are two ways of
// saying the same thing, and buildkit's path combines them (build_cmd.go, via
// common.CombineVariables). The native path read only the trailing form, so
// `--build-arg` was accepted, parsed into b.buildArgs and never looked at again:
// the build ran with the Earthfile's default and said nothing.
//
// That is this project's most recorded failure shape - a mechanism that is off
// and one that found nothing look alike - and it cost a whole experiment. The
// CI step for E602 passes `--build-arg SALT=`, so its "base that moves" never
// moved, and the measurement it existed to make has never been taken (E611).
func TestBothSpellingsOfABuildArgumentArrive(t *testing.T) {
	t.Parallel()

	got, err := nativeArgs([]string{"TRAILING=t"}, []string{"FLAG=f"})
	if err != nil {
		t.Fatalf("two well-formed arguments were refused: %v", err)
	}

	for name, want := range map[string]string{"TRAILING": "t", "FLAG": "f"} {
		if got[name] != want {
			t.Errorf("%s = %q, want %q - the whole map was %v", name, got[name], want, got)
		}
	}
}

// The trailing form wins, because that is the order buildkit combines them in:
// slices.Concat(buildFlagArgs, flagArgs), later overriding earlier. Two engines
// that disagree about precedence are worse than either rule.
func TestTheTrailingFormWinsAsItDoesForBuildkit(t *testing.T) {
	t.Parallel()

	got, err := nativeArgs([]string{"SALT=trailing"}, []string{"SALT=flag"})
	if err != nil {
		t.Fatal(err)
	}

	if got["SALT"] != "trailing" {
		t.Errorf("SALT = %q, want %q", got["SALT"], "trailing")
	}
}

// A bare name takes its value from the environment, in either spelling.
//
// **This engine does not get to invent the spelling.** `--build-arg NAME` with
// no value means "whatever the environment says", which is what `variables`
// has always done for the other backend and what this repository's own workflow
// passes: `--build-arg TAG_SUFFIX +ci-release`, with the value in the
// environment of the job. Refusing it, which this asserted until the workflow
// tried it, makes the native engine reject a command line the buildkit engine
// accepts - and the two are chosen by a flag, so a difference like that is a
// build that works until somebody switches engines.
func TestABareBuildArgumentTakesItsValueFromTheEnvironment(t *testing.T) {
	// Not parallel: t.Setenv.
	t.Setenv("FROMENV", "what the environment said")

	for _, c := range []struct{ trailing, flag []string }{
		{trailing: []string{"FROMENV"}},
		{flag: []string{"FROMENV"}},
	} {
		got, err := nativeArgs(c.trailing, c.flag)
		if err != nil {
			t.Fatalf("%v %v: %v", c.trailing, c.flag, err)
		}

		if got["FROMENV"] != "what the environment said" {
			t.Errorf("%v %v: got %q, want the environment's value",
				c.trailing, c.flag, got["FROMENV"])
		}
	}
}

// A bare name with nothing in the environment is refused rather than guessed at.
//
// The honest half of what this used to assert. An empty string is a value a
// build can legitimately be given, so defaulting to one would make "you forgot
// to export it" and "you meant it to be empty" the same command.
func TestABareBuildArgumentWithNothingBehindItIsRefused(t *testing.T) {
	// Not parallel: t.Setenv.
	t.Setenv("NOVALUE", "")
	os.Unsetenv("NOVALUE")

	for _, c := range []struct{ trailing, flag []string }{
		{trailing: []string{"NOVALUE"}},
		{flag: []string{"NOVALUE"}},
	} {
		_, err := nativeArgs(c.trailing, c.flag)
		if err == nil {
			t.Fatalf("a name with nothing behind it was accepted: %v %v", c.trailing, c.flag)
		}

		if !strings.Contains(err.Error(), "NOVALUE") {
			t.Errorf("the refusal never names the argument: %v", err)
		}
	}
}

// **`-P` reaches the engine.** It landed in the global flags and was never
// copied into the native path's options, so the interpreter saw
// `allowPrivileged=false` and refused every `RUN --privileged` - including in
// files the operator owns, where the flag is exactly the opt-in the refusal
// asks for. Eleven of fifteen Native CI jobs failed on it, reading as a policy
// decision when it was a dropped field.
//
// The same shape as the build-argument bug above: accepted, stored, never
// looked at.
func TestAllowPrivilegedReachesTheNativeEngine(t *testing.T) {
	t.Parallel()

	for _, on := range []bool{false, true} {
		got := nativeOptions(nativeInput{
			dir: ".", target: "+x", allowPrivileged: on,
		})

		if got.AllowPrivileged != on {
			t.Errorf("-P %v arrived as %v", on, got.AllowPrivileged)
		}
	}
}

// Secrets reach the native engine.
//
// **The third flag to be parsed and then dropped**, after `--build-arg` and
// `--allow-privileged`, which is why this file exists. `--secret` was worse than
// the other two: the buildkit path processes secrets at a point the native
// dispatch returns before, so `earth --engine=native --secret TOK=v` reported
// `RUN at Earthfile:5 needs the secret "TOK", which was not supplied` about a
// secret that had been supplied on the same command line.
func TestSecretsReachTheNativeEngine(t *testing.T) {
	t.Parallel()

	got := nativeOptions(nativeInput{
		dir: ".", target: "+x",
		secrets: map[string]string{"TOK": "s3cr3t", "OTHER": ""},
	})

	if got.Secrets["TOK"] != "s3cr3t" {
		t.Errorf("--secret TOK=s3cr3t arrived as %q; the whole map was %v",
			got.Secrets["TOK"], got.Secrets)
	}

	// An empty value is a secret too - `--secret FOO=` is how a caller says
	// "this exists and is blank", and dropping it turns a supplied secret into
	// a missing one.
	if _, ok := got.Secrets["OTHER"]; !ok {
		t.Errorf("--secret OTHER= did not arrive at all; the whole map was %v", got.Secrets)
	}
}

// --no-cache reaches the native engine.
//
// **The fourth flag of this kind, and the first to be silently wrong rather
// than loud.** `--secret` at least refused the build; this one returned success
// having read the cache it was told not to. Measured before the fix: a second
// `--no-cache` build of the same target reported `3 hit, 0 miss`, identical to
// the same build with no flag at all.
func TestNoCacheReachesTheNativeEngine(t *testing.T) {
	t.Parallel()

	for _, on := range []bool{false, true} {
		got := nativeOptions(nativeInput{dir: ".", target: "+x", noCache: on})

		if got.NoCache != on {
			t.Errorf("--no-cache %v arrived as %v", on, got.NoCache)
		}
	}
}
