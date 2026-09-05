package core

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step with a cache mount is placed where it will actually run.
//
// The fleet refuses to delegate one - `fleet/delegate.go` returns
// `ErrNotDelegable` for `len(op.Mounts) > 0` - so it runs on the invoker
// whatever placement decided. Placement did not know that: `eligibleFor`
// considers host-locality and platform and nothing else, so it would hand the
// step to a fleet worker, count that worker as busier, and be overruled at
// execution.
//
// **The schedule is wrong in both directions.** The worker is charged for work
// it never does and the invoker is not charged for work it does, so every later
// placement decision is made against a load map that does not describe the
// build. The green paper requires a byte-identical schedule from the same
// inputs (§4.7.3); it does not require the schedule to be *true*, and this is
// the difference (E426).
func TestACacheMountStepIsPlacedWhereItWillRun(t *testing.T) {
	t.Parallel()

	mounted := &ir.Node{
		Op: ir.Op{
			Kind: ir.OpExec, Args: []string{"make"},
			Mounts: []ir.Mount{{ID: "m2", Target: "/root/.m2"}},
		},
	}

	if eligibleFor(mounted, Worker{ID: "w2"}, ir.Platform{}) {
		t.Error("a fleet worker is eligible for a step it will refuse to run," +
			" so the schedule charges it for work it never does")
	}

	if !eligibleFor(mounted, Worker{ID: "w1", IsInvoker: true}, ir.Platform{}) {
		t.Error("the invoker is not eligible for a step only the invoker can run")
	}
}

// A step with no mount is unaffected.
//
// The guard must be about the mount, not about steps in general: an engine that
// pinned every exec to the invoker would have no fleet at all.
func TestAPlainStepIsStillPlacedAnywhere(t *testing.T) {
	t.Parallel()

	plain := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"make"}}}

	if !eligibleFor(plain, Worker{ID: "w2"}, ir.Platform{}) {
		t.Error("a step with nothing mounted was pinned to the invoker")
	}
}

// A secret is not a cache, and is refused for its own reason.
//
// Both are mounts and both stay on the invoker today, so the test says which is
// which - otherwise a later change that distributes caches would take secrets
// with it silently.
func TestASecretMountIsAlsoPlacedOnTheInvoker(t *testing.T) {
	t.Parallel()

	secret := &ir.Node{
		Op: ir.Op{
			Kind: ir.OpExec, Args: []string{"make"},
			Mounts: []ir.Mount{{ID: "token", Target: "/run/secret", Secret: true}},
		},
	}

	if eligibleFor(secret, Worker{ID: "w2"}, ir.Platform{}) {
		t.Error("a step carrying a secret was offered to a fleet worker")
	}
}

// Everything the fleet refuses to delegate is placed on the invoker.
//
// E426 fixed the cache-mount case and left three: `engine/fleet/delegate.go`
// also refuses a step needing a secret, a docker daemon or a terminal, and
// placement knew about none of them. Each is the same defect - a worker charged
// for work it will refuse, an invoker uncharged for work it will do - and each
// was invisible for the same reason: the schedule stayed deterministic, so
// nothing that checks determinism noticed (E430).
//
// Written as a table against the fleet's own list, so a fifth entry there
// without one here is a question somebody has to answer.
func TestEveryUndelegableStepIsPlacedOnTheInvoker(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		op   ir.Op
	}{
		{"a cache mount", ir.Op{Kind: ir.OpExec, Mounts: []ir.Mount{{ID: "m2"}}}},
		{"a secret", ir.Op{Kind: ir.OpExec, SecretEnv: []string{"TOKEN"}}},
		{"a docker daemon", ir.Op{Kind: ir.OpExec, Docker: true}},
		{"a terminal", ir.Op{Kind: ir.OpExec, Interactive: true}},
		{"the host", ir.Op{Kind: ir.OpHost}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			n := &ir.Node{Op: tc.op}

			if eligibleFor(n, Worker{ID: "w2"}, ir.Platform{}) {
				t.Errorf("a fleet worker is eligible for a step needing %s, which"+
					" it will refuse - so the schedule charges it for work it"+
					" never does", tc.name)
			}

			if !eligibleFor(n, Worker{ID: "w1", IsInvoker: true}, ir.Platform{}) {
				t.Errorf("the invoker is not eligible for a step only it can run (%s)",
					tc.name)
			}
		})
	}
}
