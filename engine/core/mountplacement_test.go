package core

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step with a cache mount is placed where it will actually run.
//
// **The property, and it has outlived the rule it was written for.** Placement
// and delegability have to agree: a worker charged with work it will refuse
// leaves the schedule wrong in both directions - that worker counted busier and
// the invoker not counted at all - so every later decision is made against a
// load map that does not describe the build. The green paper requires a
// byte-identical schedule (§4.7.3); it does not require the schedule to be
// *true*, and that is the difference (E426).
//
// What has changed is which side of the line a cache mount falls on. It used to
// pin the step, and no longer does: a cache is bound *over* the step's
// filesystem so its contents are excluded from the layer by construction, the
// key hashes the declaration and never the contents, and a worker with its own
// directory of the same name therefore produces the same layer. The assignment
// carries the declaration so both ends run the same operation (E433, E-F2).
//
// A **persisted** cache is the one that still pins, because its contents are
// captured and so are the result.
func TestACacheMountStepIsPlacedWhereItWillRun(t *testing.T) {
	t.Parallel()

	mounted := &ir.Node{
		Op: ir.Op{
			Kind: ir.OpExec, Args: []string{"make"},
			Mounts: []ir.Mount{{ID: "m2", Target: "/root/.m2"}},
		},
	}

	if !eligibleFor(mounted, Worker{ID: "w2"}, ir.Platform{}) {
		t.Error("a fleet worker is not eligible for a step carrying an ordinary" +
			" cache mount, so the expensive half of a real build never leaves" +
			" the invoker")
	}

	persisted := &ir.Node{
		Op: ir.Op{
			Kind: ir.OpExec, Args: []string{"make"},
			Mounts: []ir.Mount{{ID: "m2", Target: "/root/.m2", Persist: true}},
		},
	}

	if eligibleFor(persisted, Worker{ID: "w2"}, ir.Platform{}) {
		t.Error("a worker is eligible for a step whose cache is captured into" +
			" its layer, which it will refuse - so the schedule charges it for" +
			" work it never does")
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
// An ordinary cache mount used to head this list and no longer does - it is
// delegable now, and the reasoning is on the first test in this file. What
// remains are the mounts and inputs a worker genuinely cannot reproduce.
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
		{"a persisted cache", ir.Op{
			Kind: ir.OpExec, Mounts: []ir.Mount{{ID: "m2", Persist: true}},
		}},
		{"a sandbox path", ir.Op{
			Kind: ir.OpExec, Mounts: []ir.Mount{{Target: "/in", Sandbox: "/var/lib/x"}},
		}},
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
