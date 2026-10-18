package core_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/sim"
)

func stack(n int) []ir.NodeID {
	out := make([]ir.NodeID, n)
	for i := range out {
		out[i][0] = byte(i)
		out[i][1] = byte(i >> 8)
	}

	return out
}

// TestBelowTheLimitNothingHappens: flattening is a cost, so it is not paid
// until the wall is in sight.
func TestBelowTheLimitNothingHappens(t *testing.T) {
	t.Parallel()

	in := stack(100)

	out, f := core.Flatten(in, core.MaxStackDepth, core.SquashID)

	if f.Applied() {
		t.Error("flattened a stack that was already within the limit")
	}

	if len(out) != len(in) {
		t.Errorf("stack changed length: %d -> %d", len(in), len(out))
	}
}

// TestTheFiveHundredLayerWall is experiment E11 turned into a regression test.
//
// A 1,000-step target dies on today's engine at exactly step 500 with a bare
// `invalid argument` from overlayfs. It must not die here, and it must not
// merely survive: the resulting stack has to be mountable, which means at or
// under the limit.
func TestTheFiveHundredLayerWall(t *testing.T) {
	t.Parallel()

	for _, n := range []int{501, 1000, 10_000} {
		out, f := core.Flatten(stack(n), core.MaxStackDepth, core.SquashID)

		if len(out) > core.MaxStackDepth {
			t.Errorf("%d layers flattened to %d, still above the limit", n, len(out))
		}

		if !f.Applied() {
			t.Errorf("%d layers: flattening was not applied", n)
		}

		if f.Was != n {
			t.Errorf("record says the stack was %d deep, want %d", f.Was, n)
		}
	}
}

// TestTheOldestRangeIsSquashed pins the policy, not just the arithmetic.
//
// Edits land near the top of a stack, so granularity is worth most there and
// the base is what stays unchanged between builds. Squashing the newest layers
// would be arithmetically valid and would destroy precisely the cache hits that
// matter, so the choice is asserted rather than left to whoever edits next.
func TestTheOldestRangeIsSquashed(t *testing.T) {
	t.Parallel()

	in := stack(600)

	out, f := core.Flatten(in, core.MaxStackDepth, core.SquashID)

	if f.From != 0 {
		t.Errorf("squashed range starts at %d, want 0 (the oldest)", f.From)
	}

	// Everything after the cut must survive unchanged and in order.
	tail := in[f.To:]
	if len(out) != len(tail)+1 {
		t.Fatalf("expected one squashed layer plus %d survivors, got %d", len(tail), len(out))
	}

	for i, id := range tail {
		if out[i+1] != id {
			t.Fatalf("layer %d of the tail was altered by flattening", i)
		}
	}

	if out[0] != f.Into {
		t.Error("the squashed layer is not at the base of the result")
	}
}

// TestFlatteningIsDeterministic: a schedule that flattens differently between
// runs derives different keys between runs (green paper §4.7.3).
func TestFlatteningIsDeterministic(t *testing.T) {
	t.Parallel()

	in := stack(1200)

	a, fa := core.Flatten(in, core.MaxStackDepth, core.SquashID)
	b, fb := core.Flatten(in, core.MaxStackDepth, core.SquashID)

	if fa != fb {
		t.Error("two flattenings of one stack disagree")
	}

	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("flattened stacks differ at %d", i)
		}
	}
}

// TestSquashIDIsContentDerived: two builds that squash the same range must
// agree on the identity, or they cannot share the cached result.
func TestSquashIDIsContentDerived(t *testing.T) {
	t.Parallel()

	// Two ranges built separately, so the identity is checked against the
	// *value* rather than against the particular slice. Passing one slice twice
	// asserted only that the function is not reading a clock: it could have
	// hashed the address of the backing array and still passed.
	first, second := stack(50), stack(50)
	if core.SquashID(first) != core.SquashID(second) {
		t.Fatal("SquashID is not a function of its input")
	}

	rng := stack(50)

	other := stack(50)
	other[49][0] ^= 0xff

	if core.SquashID(rng) == core.SquashID(other) {
		t.Fatal("different ranges share a squash identity")
	}
}

// TestSquashIDIsDomainSeparated: a flattened layer must never collide with a
// step result or a chain key. The domain byte is what prevents it, and a
// missing domain separator is the sort of thing that is invisible until two
// key spaces overlap in production.
func TestSquashIDIsDomainSeparated(t *testing.T) {
	t.Parallel()

	single := stack(1)

	n := &ir.Node{Op: ir.Op{Kind: ir.OpExec}, Platform: amd64}

	if core.SquashID(single) == core.DeriveChainKey(n, single, nil) {
		t.Fatal("a squash identity collided with a chain key")
	}
}

// TestFlattenRefusesAnAbsurdLimit checks the degenerate case rather than
// leaving it to produce an empty stack at some later date.
func TestFlattenRefusesAnAbsurdLimit(t *testing.T) {
	t.Parallel()

	out, f := core.Flatten(stack(10), 0, core.SquashID)

	if len(out) == 0 {
		t.Fatal("flattening to a zero limit produced an empty stack")
	}

	if !f.Applied() {
		t.Error("expected flattening at a limit of zero")
	}
}

// TestDeepChainSurvivesEndToEnd is E11's failing case as an end-to-end
// scheduler test: a 1,000-step chain, which today's engine cannot build at all.
//
// It asserts three things - that it completes, that flattening was actually
// needed rather than the test being too small to matter, and that the build is
// still deterministic with Φ in the path, since flattening feeds the chain key.
func TestDeepChainSurvivesEndToEnd(t *testing.T) {
	t.Parallel()

	build := func() (string, core.Stats) {
		img := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64}

		cur := img
		for i := range 1000 {
			cur = &ir.Node{
				Op:       ir.Op{Kind: ir.OpExec, Args: []string{testLeaf, itoa(i)}},
				Platform: amd64,
				Inputs:   []*ir.Node{cur},
			}
		}

		s := newSched(newMemCache(), allBlobs{}, &sim.Executor{Seed: 11})
		_, err := s.Run(context.Background(), &ir.Graph{Root: cur})
		if err != nil {
			t.Fatalf("1,000-step chain failed: %v", err)
		}

		return "", s.Stats
	}

	_, a := build()
	if a.Flattened == 0 {
		t.Error("a 1,000-step chain did not trigger flattening; the test proves nothing")
	}

	_, b := build()

	// DeepEqual, because Stats now carries a slice - the source locations of
	// unpredicted steps. That makes this assertion *stronger*: two builds of one
	// chain must agree on those too, which is why they are sorted rather than
	// left in whatever order the scheduler happened to visit them (I12, E224).
	if !reflect.DeepEqual(a, b) {
		t.Errorf("two builds of one chain disagree: %+v vs %+v", a, b)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}

	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}

	return string(b)
}
