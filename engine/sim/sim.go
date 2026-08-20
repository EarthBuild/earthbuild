// Package sim provides the fakes the engine is built against before its real
// components exist - stage S0 of docs-internals/plan-native-engine.md.
//
// These are not scaffolding to be discarded. Every port keeps a fake for the
// life of the project: it is what lets the scheduler be exercised at a hundred
// workers with induced failures in milliseconds, deterministically, long after
// the real executor works.
//
// The fidelity contract is deliberately narrow. The simulator reproduces what
// the component under test consumes - durations, sizes, exit codes - and
// refuses to reproduce anything else. A simulator that grows fidelity nobody
// asked for becomes a second implementation, and then a second source of bugs.
package sim

import (
	"context"
	"encoding/binary"
	"sync"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Executor is a fake core.Executor: a step "runs" by yielding a duration and a
// synthetic layer, with no process, no filesystem and no bytes.
//
// What it does NOT simulate, by design: layer contents, filesystem semantics,
// mtime behaviour, isolation. Those are S3 and S4, and a green run here is not
// evidence about any of them.
type Executor struct {
	// mu guards this executor's own state: core.Executor.Run is called
	// concurrently once independent steps overlap.
	mu sync.Mutex

	// Seed makes a run reproducible. Duration and size are drawn from a
	// generator keyed by (seed, node identity), so the same seed replays the
	// same world and a different seed explores another one. This is why Clock
	// and Rand are ports rather than package functions: a test that cannot be
	// replayed from a seed cannot be debugged.
	Seed uint64

	// FailNodes forces a non-zero exit for the named nodes, so failure paths -
	// retry, propagation, WAIT/END - are reachable without a real executor.
	FailNodes map[ir.NodeID]int

	// Sleep, when true, actually waits the simulated duration. Off by default:
	// a scheduler test wants the model's arithmetic, not its latency.
	Sleep bool

	// Log records every step in execution order, which is what determinism
	// assertions compare.
	Log []Step
}

// Step is one simulated execution.
type Step struct {
	Node     ir.NodeID
	Worker   string
	Duration time.Duration
	Bytes    int64
}

// Run implements core.Executor.
func (e *Executor) Run(ctx context.Context, n *ir.Node, w core.Worker, _ []ir.NodeID, _ [][]ir.NodeID) (core.Result, error) {
	err := ctx.Err()
	if err != nil {
		return core.Result{}, err
	}

	d, size := e.estimate(n)

	if e.Sleep {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return core.Result{}, ctx.Err()
		}
	}

	exit := 0
	if code, ok := e.FailNodes[n.ID()]; ok {
		exit = code
	}

	// The log is shared across concurrently-running steps.
	e.mu.Lock()
	e.Log = append(e.Log, Step{Node: n.ID(), Worker: w.ID, Duration: d, Bytes: size})
	e.mu.Unlock()

	// Captured: the simulated layer is a deterministic function of the node,
	// which is precisely what a real capture of a deterministic step yields.
	return core.Result{Layer: n.ID(), Exit: exit, Bytes: size, Captured: true}, nil
}

// estimate derives a duration and an output size for a node.
//
// Deterministic in (seed, node identity) and in nothing else - not in wall
// time, not in map order, not in how many steps have already run. The
// distributions are crude on purpose: until build records exist there is
// nothing better to draw from, and a plausible-looking cost model would invite
// more confidence than it has earned.
func (e *Executor) estimate(n *ir.Node) (time.Duration, int64) {
	h := mix(e.Seed, n.ID())

	// Measured floors, from experiment E11: ~200 ms cold, ~16 ms warm cached.
	// A source operation is dominated by fetching; an exec by running.
	var base time.Duration

	switch n.Op.Kind {
	// OpPackImage sits here rather than in the default: writing an OCI layout
	// moves an image's worth of bytes, and the default's 100 ms was the cost
	// of a kind the model had never been told about (exhaustive).
	case ir.OpImage, ir.OpLocal, ir.OpPackImage:
		base = 400 * time.Millisecond
	case ir.OpExec, ir.OpHost, ir.OpBuild:
		base = 200 * time.Millisecond
	case ir.OpFile, ir.OpMerge:
		base = 20 * time.Millisecond
	default:
		base = 100 * time.Millisecond
	}

	// Spread of roughly 0.5x to 2.5x the base, stable per node.
	spread := time.Duration(h%2000) * base / 1000
	dur := base/2 + spread

	// Sizes: sources are large, execs middling, file ops small.
	var scale int64

	switch n.Op.Kind {
	case ir.OpImage, ir.OpLocal, ir.OpPackImage:
		scale = 64 << 20
	case ir.OpExec, ir.OpHost, ir.OpBuild:
		scale = 4 << 20
	case ir.OpFile, ir.OpMerge:
		scale = 64 << 10
	default:
		scale = 64 << 10
	}

	// The conversion is outside the modulo on purpose: `h>>32` is at most
	// 2^32-1 and fits in int64 whatever scale is, which makes the range
	// argument local instead of a claim about `scale` made somewhere else
	// (gosec G115).
	size := scale/2 + int64(h>>32)%scale

	return dur, size
}

// mix derives a stable pseudo-random value from a seed and a node identity.
// Not cryptographic and not required to be: it decides how long a fake step
// pretends to take.
func mix(seed uint64, id ir.NodeID) uint64 {
	v := seed ^ binary.BigEndian.Uint64(id[:8])
	v ^= v >> 33
	v *= 0xff51afd7ed558ccd
	v ^= v >> 33
	v *= 0xc4ceb9fe1a85ec53
	v ^= v >> 33

	return v
}
