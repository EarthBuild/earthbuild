package fleet

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestACacheMountTravelsWithItsStep.
//
// **A step run without a mount it declared is one key over two results.** The
// `Scratch` field says so for a private cache and the argument is the same for
// a shared one: what the step would have discarded into the mount it writes
// into its layer instead, and files under the invoker's key (E433).
//
// So relaxing `pinning` is not enough on its own - the wire has to carry the
// mount, or delegating a cache-mounted step is exactly the wrong answer this
// engine has been careful to avoid.
func TestACacheMountTravelsWithItsStep(t *testing.T) {
	t.Parallel()

	n := &ir.Node{
		Op: ir.Op{
			Kind: ir.OpExec,
			Args: []string{"go", "build", "./..."},
			Mounts: []ir.Mount{
				{Target: "/go/pkg/mod", ID: "go-mod"},
				{Target: "/root/.cache/go-build", ID: "go-build", Exclusive: true},
				{Target: "/tmp/scratch", Ephemeral: true},
			},
		},
	}

	a, err := Delegate(n, nil, nil)
	if err != nil {
		t.Fatalf("a step with a cache mount was refused: %v", err)
	}

	if len(a.Op.Caches) != 2 {
		t.Fatalf("the assignment carries %d cache(s), want 2 - a step run"+
			" without a mount it declared writes into its layer what it would"+
			" have discarded", len(a.Op.Caches))
	}

	byID := map[string]Cache{}
	for _, c := range a.Op.Caches {
		byID[c.ID] = c
	}

	if got := byID["go-mod"]; got.Target != "/go/pkg/mod" {
		t.Errorf("go-mod is mounted at %q", got.Target)
	}

	if !byID["go-build"].Exclusive {
		t.Error("--sharing=locked was lost on the way, so a worker runs" +
			" several steps in a directory the build said was for one")
	}

	// The private one still travels as scratch, and is not confused with a
	// shared cache that outlives the step.
	if len(a.Op.Scratch) != 1 || a.Op.Scratch[0] != "/tmp/scratch" {
		t.Errorf("the private cache travelled as %v", a.Op.Scratch)
	}

	// And the worker rebuilds exactly what the invoker had.
	op, err := operationOf(a.Op)
	if err != nil {
		t.Fatalf("rebuilding: %v", err)
	}

	var shared, ephemeral int

	for _, m := range op.Mounts {
		if m.Ephemeral {
			ephemeral++

			continue
		}

		if m.ID != "" {
			shared++
		}
	}

	if shared != 2 || ephemeral != 1 {
		t.Errorf("the worker rebuilt %d shared and %d private mount(s), want 2 and 1",
			shared, ephemeral)
	}
}
