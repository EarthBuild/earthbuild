package core_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// An observation of a base that names nothing in it is not an observation.
//
// The rule was stated on the step's *kind*: an exec step must have seen
// something, anything else need not. That was right about the case in front of
// it (E112) and wrong as a rule. What makes an empty observation dangerous is
// not the opcode - it is that `Consistent` iterates three empty collections and
// returns true for **every base in existence**, so Κ₂ claims the result is
// valid wherever the step runs.
//
// A COPY has a base and reads its destination in it (E119). A step with *no*
// base genuinely reads nothing from one, and refusing its observation would be
// the mirror mistake.
//
// So the question is the base, not the kind: **a step that stood on something
// and reports looking at none of it did not observe.** Stating it that way also
// makes it true for the next opcode without anybody revisiting this.
func TestAnObservationOfANonEmptyBaseMustNameSomething(t *testing.T) {
	t.Parallel()

	base := []ir.NodeID{digest(1)}

	for _, tc := range []struct {
		name string
		kind ir.OpKind
		base []ir.NodeID
		obs  core.Observation
		want bool
	}{{
		name: "a copy over a base, having looked at nothing",
		kind: ir.OpFile,
		base: base,
		obs:  core.Observation{},
		want: false,
	}, {
		name: "a copy over a base, having looked at its destination",
		kind: ir.OpFile,
		base: base,
		obs:  core.Observation{Negative: []string{"/dest"}},
		want: true,
	}, {
		name: "an exec over a base, having looked at nothing",
		kind: ir.OpExec,
		base: base,
		obs:  core.Observation{},
		want: false,
	}, {
		// The mirror mistake. An image step has no base to read from, and
		// refusing it would make the rule wrong in the other direction.
		name: "a step with no base at all",
		kind: ir.OpImage,
		base: nil,
		obs:  core.Observation{},
		want: true,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			n := &ir.Node{Op: ir.Op{Kind: tc.kind, Args: []string{"x"}}}

			if got := core.ObservesSomething(n, tc.base, tc.obs); got != tc.want {
				t.Errorf("reported %v, want %v", got, tc.want)
			}
		})
	}
}
