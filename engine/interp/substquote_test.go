package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A substituted command keeps its quoting, because a shell is going to read it.
//
// `$(md5sum /etc/os-release | cut -d' ' -f 1)` is an ordinary line - the
// delimiter is a space, and the only way to say so is to quote it. This engine
// resolved the quotes before running the command and then split the result on
// whitespace, so the shell was handed:
//
//	cut -d -f 1
//
// which is `cut` being told the delimiter is `-f`. It failed with the kindest
// possible message and it was still two Earthfiles away from anything anybody
// wrote:
//
//	cut: the delimiter must be a single character
//
// The rule is the one this engine already applies to a RUN: text a shell will
// parse again keeps its quoting, and only what the *engine* consumes has its
// quoting resolved (E65).
func TestASubstitutedCommandKeepsItsQuoting(t *testing.T) {
	t.Parallel()

	r := &recorder{result: true, output: "ok"}

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    ARG digest = $(md5sum /etc/os-release | cut -d' ' -f 1)
    RUN echo $digest
`, testMain, interp.WithCommands(r.run))
	if err != nil {
		t.Fatal(err)
	}

	if len(r.calls) == 0 {
		t.Fatal("the substitution never reached the runner")
	}

	got := strings.Join(r.calls[0], " ")

	if !strings.Contains(got, "-d' '") {
		t.Errorf("the quoted delimiter did not survive:\n  got  %s\n  want it to contain -d' '", got)
	}
}

// An argument the engine substitutes still reaches the command.
//
// The other half: keeping the quoting must not mean keeping `$name` too. A
// declared argument is the engine's to expand, and a command that received the
// text `$version` would run something nobody wrote.
func TestASubstitutedCommandStillGetsItsArguments(t *testing.T) {
	t.Parallel()

	r := &recorder{result: true, output: "ok"}

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    ARG version = 3.22
    ARG line = $(grep "alpine $version" /etc/os-release)
    RUN echo $line
`, testMain, interp.WithCommands(r.run))
	if err != nil {
		t.Fatal(err)
	}

	if len(r.calls) == 0 {
		t.Fatal("the substitution never reached the runner")
	}

	got := strings.Join(r.calls[0], " ")

	// The quotes as well as the value: `grep alpine 3.22 file` is grep being
	// asked for the pattern `alpine` in the files `3.22` and `file`, which is a
	// different command that happens to run.
	if !strings.Contains(got, `"alpine 3.22"`) {
		t.Errorf("the quoted argument did not survive:\n  got %s", got)
	}

	if strings.Contains(got, "$version") {
		t.Errorf("the argument was left unexpanded:\n  got %s", got)
	}
}
