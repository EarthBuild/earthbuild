package fleet_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// recordingExec keeps the node it was asked to run.
type recordingExec struct {
	got *ir.Node
	res core.Result
	err error
}

func (r *recordingExec) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	r.got = n

	return r.res, r.err
}

// An assignment becomes the step it describes.
//
// The mirror of `Delegate`, and the direction where care is needed: an
// assignment arrives **from somebody else**, so what it becomes has to be
// derived rather than trusted.
func TestAnAssignmentBecomesTheStepItDescribes(t *testing.T) {
	t.Parallel()

	e := &recordingExec{res: core.Result{Layer: ir.NodeID{4}}}
	run := fleet.Runner(e, core.Worker{ID: "w"})

	_, err := run(t.Context(), fleet.Assignment{
		Version: fleet.Version,
		Base:    []ir.NodeID{{1}},
		Op: fleet.Op{
			Kind: fleet.KindExec, Args: []string{"make", "-j4"},
			Env: map[string]string{"CC": "gcc"}, Dir: "/src", User: "build",
			NoNetwork: true,
		},
		Platform: "linux/arm64/v8",
	})
	if err != nil {
		t.Fatal(err)
	}

	got := e.got
	if got == nil {
		t.Fatal("nothing was run")
	}

	if got.Op.Kind != ir.OpExec {
		t.Errorf("the operation is %v, want exec", got.Op.Kind)
	}

	if got.Op.Dir != "/src" || got.Op.User != "build" || !got.Op.NoNetwork ||
		got.Op.Env["CC"] != "gcc" || len(got.Op.Args) != 2 {
		t.Errorf("the operation did not survive: %+v", got.Op)
	}

	if got.Platform.OS != "linux" || got.Platform.Arch != "arm64" ||
		got.Platform.Variant != "v8" {
		t.Errorf("the platform arrived as %+v", got.Platform)
	}
}

// What this worker does not understand, it refuses by name.
//
// A switch with no default that guesses. A kind this engine does not know is a
// message it cannot interpret, and running something a peer named and this
// engine read differently is the failure the whole vocabulary exists to prevent.
//
// Each refusal **says what is missing**, so the driver's message names the gap
// rather than reporting that something went wrong.
func TestWhatAWorkerCannotReadItRefusesByName(t *testing.T) {
	t.Parallel()

	e := &recordingExec{}
	run := fleet.Runner(e, core.Worker{ID: "w"})

	for _, tc := range []struct {
		name string
		a    fleet.Assignment
		says string
	}{
		{
			name: "a kind it has never heard of",
			a: fleet.Assignment{
				Version: fleet.Version, Op: fleet.Op{Kind: "sudo"},
			},
			says: "sudo",
		},
		{
			name: "a whole target",
			a: fleet.Assignment{
				Version: fleet.Version, Op: fleet.Op{Kind: fleet.KindBuild},
			},
			says: "whole target",
		},
		{
			name: "a version it does not speak",
			a: fleet.Assignment{
				Version: fleet.Version + 1, Op: fleet.Op{Kind: fleet.KindExec},
			},
			says: "version",
		},
	} {
		reply, err := run(t.Context(), tc.a)
		if err != nil {
			t.Errorf("%s: returned an error rather than a refusal: %v", tc.name, err)

			continue
		}

		if reply.Refused == "" {
			t.Errorf("%s: was accepted", tc.name)

			continue
		}

		if !strings.Contains(reply.Refused, tc.says) {
			t.Errorf("%s: refused with %q, which does not name what was wrong",
				tc.name, reply.Refused)
		}

		if e.got != nil {
			t.Errorf("%s: something ran anyway", tc.name)
		}
	}
}

// A step that ran and failed is a result; one that could not run is a refusal.
//
// E232's distinction, from the worker's side. A non-zero exit travels as an exit
// so the driver fails the build with its output; a sandbox that would not boot
// travels as a refusal so the driver runs the step elsewhere. Collapsing them
// would either hide a user's error or run a failing step on every machine.
func TestAFailedStepIsAResultAndABrokenWorkerIsARefusal(t *testing.T) {
	t.Parallel()

	failed := &recordingExec{res: core.Result{Layer: ir.NodeID{2}, Exit: 3}}
	reply, err := fleet.Runner(failed, core.Worker{ID: "w"})(t.Context(),
		fleet.Assignment{Version: fleet.Version, Op: fleet.Op{Kind: fleet.KindExec}})
	if err != nil {
		t.Fatal(err)
	}

	if reply.Exit != 3 || reply.Refused != "" {
		t.Errorf("a step that ran and failed became %+v; the build should fail"+
			" with its output rather than move to another machine", reply)
	}

	broken := &recordingExec{err: errors.New("the sandbox would not start")}
	reply, err = fleet.Runner(broken, core.Worker{ID: "w"})(t.Context(),
		fleet.Assignment{Version: fleet.Version, Op: fleet.Op{Kind: fleet.KindExec}})
	if err != nil {
		t.Fatal(err)
	}

	if reply.Refused == "" {
		t.Errorf("a worker that could not start the step reported %+v; the"+
			" driver should run it somewhere that can", reply)
	}
}
