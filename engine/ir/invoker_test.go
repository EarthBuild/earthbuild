package ir_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A mount pins a step to the invoker when it names something on the invoker.
//
// The rule was "any mount at all", which is true of a *named* cache: its
// contents are on this machine and a worker would run the step against an empty
// directory it thinks is warm. It is not true of `--sharing=private`, which
// names nothing: the guest makes the directory for the step and removes it
// afterwards (§3.3c), so every machine can produce one and they are the same
// directory - empty.
//
// The pin costs a real build most of its fleet. A cargo or npm build puts a
// CACHE in nearly every RUN, so "any mount pins" is, for those builds, "nothing
// is ever delegated" - the fleet works perfectly and does nothing (E433).
func TestWhichMountsPinAStepToTheInvoker(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		mount ir.Mount
		pins  bool
		why   string
	}{{
		name:  "a named cache",
		mount: ir.Mount{Target: "/cache", ID: "cargo", Exclusive: true},
		pins:  true,
		why:   "its contents are on the invoking machine",
	}, {
		name:  "a shared cache",
		mount: ir.Mount{Target: "/cache", ID: "npm"},
		pins:  true,
		why:   "its contents are on the invoking machine",
	}, {
		name:  "a private cache",
		mount: ir.Mount{Target: "/cache", Ephemeral: true},
		pins:  false,
	}, {
		// `--persist` writes the cache's contents *into* the layer, so it is not
		// the same step as one that discards them - and an assignment carries a
		// target and nothing else, so a worker could not tell. Portable means
		// exactly `{Target, Ephemeral}` and everything else stays home, which is
		// the safe direction as fields get added.
		name:  "a private cache that persists",
		mount: ir.Mount{Target: "/cache", Ephemeral: true, Persist: true},
		pins:  true,
	}, {
		name: "a secret, however scratch-like",
		// A secret mount is a credential staged per step, which looks ephemeral
		// and is not: the value is on the invoking machine and an assignment
		// does not carry it. Ordering matters here, which is why it is a case.
		mount: ir.Mount{Target: "/run/secrets/x", Secret: true, Ephemeral: true},
		pins:  true,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			op := ir.Op{Kind: ir.OpExec, Args: []string{"make"}}
			op.Mounts = []ir.Mount{tc.mount}

			pins, why := op.OnInvokerOnly()

			if pins != tc.pins {
				t.Fatalf("%s: pins=%v, want %v (reason %q)", tc.name, pins, tc.pins, why)
			}

			if pins && why == "" {
				t.Error("pinned with no reason given" +
					"\n  the scheduler prints this, so an empty one is a step that" +
					" declines to say why it stayed home")
			}
		})
	}
}

// The reason names the mount that did it.
//
// One CACHE among five is what keeps a step home, and "it needs a cache mount"
// leaves the author to find which - having already been told, in the same build,
// which cache made a step uncacheable (E432's whyUncacheable). A diagnostic that
// stops one word short of actionable is the failure class this project keeps
// re-finding.
func TestThePinNamesTheMountResponsible(t *testing.T) {
	t.Parallel()

	op := ir.Op{Kind: ir.OpExec, Args: []string{"make"}}
	op.Mounts = []ir.Mount{
		{Target: "/scratch", Ephemeral: true},
		{Target: "/root/.cargo", ID: "cargo-registry"},
	}

	_, why := op.OnInvokerOnly()

	if !contains(why, "cargo-registry") {
		t.Errorf("the reason is %q, which does not name the cache that did it", why)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}

	return false
}
