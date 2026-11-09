package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A probe on a host anchor runs on the host, where the build is, and nothing
// else runs with it.
//
// The anchor is how the interpreter says "LOCALLY, with nothing before this on
// the host": a host step with no command. Running the probe as a sandbox step
// would answer about an image; running the anchor's container inputs would
// build an image nobody asked for.
func TestALocallyProbeRunsOnTheHost(t *testing.T) {
	t.Parallel()

	anchor := &ir.Node{Op: ir.Op{
		Kind: ir.OpHost, Dir: "sub", HostAbs: false,
		Env: map[string]string{"MODE": "release"},
	}}

	var probe *ir.Node

	run := func(_ context.Context, g *ir.Graph) (string, error) {
		probe = g.Root

		return "out\n", nil
	}

	res, err := decideByRunning(context.Background(), run,
		[]string{"git", "rev-parse", "HEAD"}, anchor, "sub", "Earthfile:4")
	if err != nil {
		t.Fatalf("a LOCALLY probe was refused: %v", err)
	}

	if probe.Op.Kind != ir.OpHost {
		t.Errorf("the probe is a %v step, want a host step", probe.Op.Kind)
	}

	if got := strings.Join(probe.Op.Args, " "); got != "/bin/sh -c git rev-parse HEAD" {
		t.Errorf("the probe runs %q", got)
	}

	if probe.Op.Dir != "sub" || probe.Op.Env["MODE"] != "release" || len(probe.Inputs) != 0 {
		t.Errorf("the probe is not where the build is, alone: dir %q env %v inputs %d",
			probe.Op.Dir, probe.Op.Env, len(probe.Inputs))
	}

	if res.Output != "out\n" {
		t.Errorf("the value is %q", res.Output)
	}
}

// After a host RUN the anchor stands on it, and the probe is still refused:
// deciding would run that RUN a second time (I7).
func TestALocallyProbeAfterAHostRunIsRefused(t *testing.T) {
	t.Parallel()

	anchor := &ir.Node{
		Op:     ir.Op{Kind: ir.OpHost},
		Inputs: []*ir.Node{{Op: ir.Op{Kind: ir.OpHost, Args: []string{"touch", "f"}}}},
	}

	_, err := decideByRunning(context.Background(),
		func(context.Context, *ir.Graph) (string, error) { return "", nil },
		[]string{"cat", "f"}, anchor, "", "Earthfile:5")
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("a probe after a host RUN was not refused for running it twice: %v", err)
	}
}
