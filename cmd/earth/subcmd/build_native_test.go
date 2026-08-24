package subcmd

import (
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

// A bare name is refused rather than guessed at, in either spelling.
func TestABuildArgumentWithoutAValueIsRefused(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ trailing, flag []string }{
		{trailing: []string{"NOVALUE"}},
		{flag: []string{"NOVALUE"}},
	} {
		_, err := nativeArgs(c.trailing, c.flag)
		if err == nil {
			t.Fatalf("a bare name was accepted: %v %v", c.trailing, c.flag)
		}

		if !strings.Contains(err.Error(), "NOVALUE") {
			t.Errorf("the refusal never names the argument: %v", err)
		}
	}
}
