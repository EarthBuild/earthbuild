package fleet_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// An executor that can be recognised again, and counts what it was asked.
type countingExecutor struct{ runs int }

func (c *countingExecutor) Run(
	_ context.Context, _ *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	c.runs++

	return core.Result{}, nil
}

// No fleet configured leaves the build exactly as it was.
//
// The overwhelmingly common case: somebody building on a laptop. If configuring
// no fleet produced a *wrapper* rather than the executor itself, every build
// everywhere would take the delegating path and differ from the one this engine
// was tested with - a cost paid by everyone to serve nobody. So the seam returns
// the very executor it was handed, and this asserts identity rather than
// behaviour, because behaviour would pass for a wrapper too.
func TestNoFleetConfiguredLeavesTheBuildExactlyAsItWas(t *testing.T) {
	local := &countingExecutor{}

	got, stop, err := fleet.Driver(t.Context(), local, nil, nil, nil)
	if err != nil {
		t.Fatalf("no fleet configured must not be an error: %v", err)
	}

	if stop != nil {
		t.Error("nothing was started, so there is nothing to stop")
	}

	if got != core.Executor(local) {
		t.Errorf("got %T, want the executor that was passed in"+
			"\n  an unconfigured build must not take the delegating path", got)
	}
}

// A driver whose workers never arrive builds locally, and says so.
//
// I11 asks for refuse-or-degrade, and this is the degrade: the build still
// happens, just on one machine. Refusing would be worse - a CI job whose worker
// pool failed to start would fail the build rather than run it slowly - and
// waiting for a worker that is never coming would hang it, which is worse still.
//
// What is *not* acceptable is doing it quietly: a fleet build that silently
// became a local one looks like a slow fleet, and somebody spends an afternoon
// on the network.
func TestADriverWithNoWorkersDegradesToLocalAndSaysSo(t *testing.T) {
	t.Setenv(fleet.EnvSecret, "shared-secret")
	t.Setenv(fleet.EnvSession, "degrade-test")
	t.Setenv(fleet.EnvWorkers, "2")
	t.Setenv(fleet.EnvWait, "200ms")

	var said []string

	local := &countingExecutor{}
	start := time.Now()

	got, stop, err := fleet.Driver(t.Context(), local,
		func(s string) { said = append(said, s) }, nil, nil)
	if err != nil {
		t.Fatalf("a fleet nobody joined must not fail the build: %v", err)
	}

	if stop != nil {
		defer stop()
	}

	took := time.Since(start)
	if took > 10*time.Second {
		t.Errorf("waited %v for workers that were never coming;"+
			" the wait is bounded by %s, not by the count", took, fleet.EnvWait)
	}

	if got != core.Executor(local) {
		t.Errorf("got %T; with no workers the build must run locally"+
			" rather than through an empty fleet", got)
	}

	joined := strings.Join(said, "\n")
	if !strings.Contains(joined, "0 of 2") {
		t.Errorf("said %q\n  a degraded build must name what it expected and"+
			" what it got, or it looks like a slow fleet", joined)
	}
}

// A count that is not a number is refused rather than assumed.
//
// Assuming zero would turn a typo into a silently local build - the exact
// outcome the loud degrade above exists to make visible.
func TestAnUnreadableWorkerCountIsRefused(t *testing.T) {
	t.Setenv(fleet.EnvSecret, "shared-secret")
	t.Setenv(fleet.EnvWorkers, "lots")

	_, _, err := fleet.Driver(t.Context(), &countingExecutor{}, nil, nil, nil)
	if err == nil {
		t.Fatal("an unreadable worker count was accepted")
	}

	if !strings.Contains(err.Error(), fleet.EnvWorkers) {
		t.Errorf("%v\n  the message must name the variable to fix", err)
	}
}

// Asking for no workers at all is a local build, not a fleet of nobody.
//
// Somebody who sets a secret in CI for a later step, and no worker count, gets
// the build they had. Binding a driver and waiting for a pool that was never
// requested would cost every such build a bind and a timeout.
func TestASecretWithoutAWorkerCountIsStillALocalBuild(t *testing.T) {
	t.Setenv(fleet.EnvSecret, "shared-secret")

	local := &countingExecutor{}

	got, stop, err := fleet.Driver(t.Context(), local, nil, nil, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if stop != nil {
		defer stop()
	}

	if got != core.Executor(local) {
		t.Errorf("got %T, want the local executor", got)
	}
}
