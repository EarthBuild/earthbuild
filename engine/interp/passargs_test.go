package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `--pass-args` is a dialect the file has to ask for.
//
// The engine implemented the flag on BUILD, FROM and COPY and gated it on
// nothing, so an Earthfile using it without saying so on its VERSION line built
// here and would be refused by the reference. That is the quiet way a
// compatible implementation stops being one, and it is the failure this file's
// whole feature mechanism exists to prevent - `--try` is gated exactly so.
//
// Found by planning the repository's own targets: eight of them go through
// `internal/earthfile/tests/Earthfile`, whose VERSION line asks for
// `--pass-args`, and this engine did not know the flag existed (E63).
func TestPassArgsNeedsTheFeature(t *testing.T) {
	t.Parallel()

	// 0.7, where the flag is still opt-in. At 0.8 it is the default and this
	// construct is allowed without it - see the test below, and defaultsFor.
	src := `VERSION 0.7

lib:
    FROM alpine:3.22
    ARG greeting=none
    RUN echo $greeting

build:
    FROM alpine:3.22
    ARG greeting=hello
    BUILD --pass-args +lib
`

	_, err := interp.Build(src, "build", interp.WithPlatform("linux/arm64"))
	if err == nil {
		t.Fatal("--pass-args was accepted on a file that never asked for it")
	}

	// The *gate's* wording, not the unknown-flag one. Before the flag existed
	// this test passed against "VERSION --pass-args is a feature this engine
	// does not know", which mentions both `--pass-args` and `VERSION` and says
	// nothing about the construct being gated - a green run about the wrong
	// refusal.
	if !strings.Contains(err.Error(), "needs the --pass-args feature") {
		t.Errorf("the refusal is not the gate's:\n%v", err)
	}

	if !strings.Contains(err.Error(), "BUILD") {
		t.Errorf("the refusal does not name the construct:\n%v", err)
	}
}

// And with the feature declared, it works.
//
// The other half: a gate that refuses everything is not a gate. The argument
// has to reach the built target, which is what the flag is for.
func TestPassArgsWorksWhenDeclared(t *testing.T) {
	t.Parallel()

	src := `VERSION --pass-args 0.8

lib:
    FROM alpine:3.22
    ARG greeting=none
    RUN echo "lib says $greeting"

build:
    FROM alpine:3.22
    ARG greeting=hello
    BUILD --pass-args +lib
`

	p, err := interp.Build(src, "build", interp.WithPlatform("linux/arm64"))
	if err != nil {
		t.Fatal(err)
	}

	var found bool

	for _, n := range p.Graph.Nodes() {
		if strings.Contains(strings.Join(n.Op.Args, " "), "lib says hello") {
			found = true
		}
	}

	if !found {
		t.Error("the caller's argument did not reach the target it built")
	}
}

// The flags this engine understands and does not gate are accepted.
//
// A file naming one is not written for a dialect we lack: `--arg-scope-and-set`
// asks for SET, which this engine has, and `--docker-cache` for a WITH DOCKER
// cache it either has or refuses by name elsewhere. Refusing the file over a
// feature it may not even use is the failure in the other direction.
func TestKnownButUngatedFeaturesAreAccepted(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"--arg-scope-and-set", "--docker-cache", "--pass-args"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()

			src := "VERSION " + flag + ` 0.8

build:
    FROM alpine:3.22
    RUN echo hello
`

			_, err := interp.Build(src, "build", interp.WithPlatform("linux/arm64"))
			if err != nil {
				t.Errorf("VERSION %s was refused: %v", flag, err)
			}
		})
	}
}

// At 0.8 the flag is the default, and a file that does not name it still works.
//
// This is the half that a gate on the flag alone gets wrong, and the evidence
// is in this repository: the root Earthfile uses `BUILD --pass-args` under a
// bare `VERSION 0.8`, and the reference builds it. Two other files here declare
// the flag at 0.7, which is what puts the boundary between them.
func TestPassArgsIsTheDefaultAtEightPointZero(t *testing.T) {
	t.Parallel()

	src := `VERSION 0.8

lib:
    FROM alpine:3.22
    ARG greeting=none
    RUN echo "lib says $greeting"

build:
    FROM alpine:3.22
    ARG greeting=hello
    BUILD --pass-args +lib
`

	p, err := interp.Build(src, "build", interp.WithPlatform("linux/arm64"))
	if err != nil {
		t.Fatalf("--pass-args was refused at 0.8, where it is the default: %v", err)
	}

	var found bool

	for _, n := range p.Graph.Nodes() {
		if strings.Contains(strings.Join(n.Op.Args, " "), "lib says hello") {
			found = true
		}
	}

	if !found {
		t.Error("the argument did not reach the built target")
	}
}

// And TRY is still opt-in at 0.8, so the default is not "everything".
//
// Five Earthfiles here write `VERSION --try 0.8`, which they would not need if
// 0.8 implied it. A defaults table that turned on every flag would accept files
// the reference refuses, which is the fault this mechanism exists to prevent -
// in the opposite direction from the one that caused it.
func TestTryIsStillOptInAtEightPointZero(t *testing.T) {
	t.Parallel()

	src := `VERSION 0.8

build:
    FROM alpine:3.22
    TRY
        RUN false
    FINALLY
        RUN echo done
    END
`

	_, err := interp.Build(src, "build", interp.WithPlatform("linux/arm64"))
	if err == nil {
		t.Fatal("TRY was accepted at 0.8 without --try")
	}

	if !strings.Contains(err.Error(), "--try") {
		t.Errorf("the refusal does not name the flag:\n%v", err)
	}
}

// The refusal for an unknown flag says the same thing twice running.
//
// It lists the flags this engine gates on, and that list came straight out of a
// map - so with one known feature it was stable, and the moment `--pass-args`
// made it two, the same Earthfile produced two different error messages
// depending on Go's map order.
//
// That is what `TestPlanningIsDeterministic` had been catching intermittently
// since the feature was added: not a plan that differed, but a *refusal* that
// did. A message is part of what a build produces (I12), and one that varies
// makes every tool that diffs two builds report noise (E66).
func TestTheUnknownFlagRefusalIsStable(t *testing.T) {
	t.Parallel()

	src := `VERSION --no-such-flag 0.8

build:
    FROM alpine:3.22
    RUN echo hello
`

	first := ""

	for range 20 {
		_, err := interp.Build(src, "build", interp.WithPlatform("linux/arm64"))
		if err == nil {
			t.Fatal("an unknown VERSION flag was accepted")
		}

		if first == "" {
			first = err.Error()

			continue
		}

		if err.Error() != first {
			t.Fatalf("the same file was refused two ways:\n  %s\n  %s", first, err.Error())
		}
	}
}
