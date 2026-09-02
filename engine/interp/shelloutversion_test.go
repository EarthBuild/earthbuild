package interp_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Before 0.7, a `$(...)` is expanded only as the whole value of an `ARG`.
//
// `tests/shell-out` states the rule four times over and names a file after it.
// Every `old*.earth` there opens `VERSION 0.6 # do not change to 0.7; this test
// is for old functionality`, and between them they pin all three halves of the
// old behaviour:
//
//   - `old.earth` expands `ARG k = $( echo ... )` and `ARG k = "$( echo ... )"`,
//     so the whole value counts with or without one layer of quotes;
//   - `old-no-middle-shell-out.earth` writes `ARG key="hello$(cat /data)"` and
//     asserts the value is that text, so a substitution anywhere else is not one;
//   - `old-fail1.earth` writes `SAVE ARTIFACT "valid-$(echo file)"` and expects
//     the build to fail on a *missing file of that name*, so the rule is about
//     `ARG` and not about values in general.
//
// `--shell-out-anywhere` is the flag that lifts it and 0.7 turns it on. This
// engine had the flag in `ignoredFeatures` - accepted and not acted on - so
// every version expanded everywhere, and `+test-no-qemu-group7` failed on the
// first of those files to be reached (E957).
func TestShellOutBeforeSevenIsTheWholeArgValueOnly(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, version, body string
		wantRun             []string
	}{{
		name:    "the whole value, unquoted",
		version: "VERSION 0.6",
		body:    "    ARG k = $(echo hi)\n    RUN echo $k\n",
		wantRun: []string{"echo hi"},
	}, {
		name:    "the whole value in one layer of quotes",
		version: "VERSION 0.6",
		body:    "    ARG k = \"$(echo hi)\"\n    RUN echo $k\n",
		wantRun: []string{"echo hi"},
	}, {
		name:    "a substitution in the middle of a value is text",
		version: "VERSION 0.6",
		body:    "    ARG k = \"hello$(echo hi)\"\n    RUN echo $k\n",
		wantRun: nil,
	}, {
		name:    "a substitution in a command is text",
		version: "VERSION 0.6",
		body:    "    RUN echo $(echo hi)\n",
		wantRun: nil,
	}, {
		// The case `old-fail1.earth` states, and it states it by expecting a
		// *failure*: `SAVE ARTIFACT "valid-$(echo file)"` at 0.6 must look for a
		// file of that literal name. Running the command instead makes the
		// artifact exist and the build succeed, which is the assertion inverted.
		name:    "a substitution in a value the engine consumes is text",
		version: "VERSION 0.6",
		body:    "    RUN touch valid-file\n    SAVE ARTIFACT \"valid-$(echo hi)\"\n",
		wantRun: nil,
	}, {
		name:    "the same value from 0.7 is a substitution",
		version: "VERSION 0.7",
		body:    "    RUN touch valid-file\n    SAVE ARTIFACT \"valid-$(echo hi)\"\n",
		wantRun: []string{"echo hi"},
	}, {
		name:    "0.7 turns the flag on by itself",
		version: "VERSION 0.7",
		body:    "    ARG k = \"hello$(echo hi)\"\n    RUN echo $k\n",
		wantRun: []string{"echo hi"},
	}, {
		name:    "an older file may ask for it by name",
		version: "VERSION --shell-out-anywhere 0.6",
		body:    "    ARG k = \"hello$(echo hi)\"\n    RUN echo $k\n",
		wantRun: []string{"echo hi"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var ran []string

			run := func(cmd []string, _ *ir.Node, _, _ string) (interp.Result, error) {
				ran = append(ran, strings.Join(cmd, " "))

				return interp.Result{Output: "hi\n"}, nil
			}

			_, err := interp.Build(tc.version+"\n\nmain:\n    FROM alpine:3.22\n"+tc.body,
				"main", interp.WithCommands(run))
			if err != nil {
				t.Fatalf("planning: %v", err)
			}

			if !slices.Equal(ran, tc.wantRun) {
				t.Errorf("the engine ran %q, want %q", ran, tc.wantRun)
			}
		})
	}
}

// Before 0.7 a shell-out that fails leaves the argument empty.
//
// `tests/shell-out/old-ignore-shellout-errors.earth` is named for it and says so
// in a comment: `ARG key2 = $(invalid-command) # this will fail with
// --shell-out-anywhere`, followed by `RUN env | grep '^key2=$'`. So the old
// behaviour is to swallow the failure and the new one is to report it - which is
// what the flag's name is about, once you read the file it was written for.
func TestAFailedShellOutBeforeSevenIsAnEmptyValue(t *testing.T) {
	t.Parallel()

	run := func([]string, *ir.Node, string, string) (interp.Result, error) {
		return interp.Result{Exit: 127, Output: "sh: invalid-command: not found\n"}, nil
	}

	p, err := interp.Build("VERSION 0.6\n\nmain:\n    FROM alpine:3.22\n"+
		"    ARG k1=apple\n    ARG k2 = $(invalid-command)\n    RUN echo \"$k1/$k2\"\n",
		"main", interp.WithCommands(run))
	if err != nil {
		t.Fatalf("a failing shell-out at 0.6 stopped the build: %v", err)
	}

	var step string

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec && len(n.Op.Args) == 3 {
			step = n.Op.Args[2]
		}
	}

	if want := `echo "apple/"`; step != want {
		t.Errorf("the step runs %q, want %q", step, want)
	}

	// And at 0.7 the same failure is reported, which is the other half of the
	// flag and the half a test of the old behaviour alone would not pin.
	_, err = interp.Build("VERSION 0.7\n\nmain:\n    FROM alpine:3.22\n"+
		"    ARG k2 = $(invalid-command)\n    RUN echo $k2\n",
		"main", interp.WithCommands(run))
	if err == nil {
		t.Error("a failing shell-out at 0.7 was swallowed")
	}
}
