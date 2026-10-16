package core_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// oneTree is a store that will say what any stack materialises to, so Κₜ is
// derivable and the guard below is measuring the operation rather than the
// store's willingness to answer.
type oneTree struct{}

func (oneTree) Has(ir.NodeID) bool { return true }

func (oneTree) TreeOf([]ir.NodeID) (ir.NodeID, bool) { return ir.NodeID{42}, true }

// The ambient state a step runs in reaches every key too.
//
// Not a field of ir.Op, and so not covered by the walk above: the platform
// lives on the node. Κₜ carries it as named properties rather than in the
// operation digest, which is a second path and therefore a second thing that
// can be forgotten.
func TestThePlatformReachesEveryKey(t *testing.T) {
	t.Parallel()

	base := []ir.NodeID{{1}}
	obs := core.Observation{Reads: map[string]ir.NodeID{"/bin/sh": {7}}}

	for _, c := range []struct {
		name string
		a, b ir.Platform
	}{
		{"OS", ir.Platform{OS: "linux", Arch: "arm64"}, ir.Platform{OS: "darwin", Arch: "arm64"}},
		{"Arch", ir.Platform{OS: "linux", Arch: "arm64"}, ir.Platform{OS: "linux", Arch: "amd64"}},
		{"Variant", ir.Platform{OS: "linux", Arch: "arm", Variant: "v7"}, ir.Platform{OS: "linux", Arch: "arm", Variant: "v6"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			x := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"go", "build"}}, Platform: c.a}
			y := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"go", "build"}}, Platform: c.b}

			if core.DeriveChainKey(x, base, nil) == core.DeriveChainKey(y, base, nil) {
				t.Errorf("two platforms differing in %s share Κ₁", c.name)
			}

			if core.DeriveObservedKey(x, nil, obs) == core.DeriveObservedKey(y, nil, obs) {
				t.Errorf("two platforms differing in %s share Κ₂"+
					"\n  a step built for one would be served the other's result", c.name)
			}

			kx, okx := core.DeriveContentKey(x, base, nil, oneTree{})
			ky, oky := core.DeriveContentKey(y, base, nil, oneTree{})

			if !okx || !oky {
				t.Fatal("Κₜ was not derivable")
			}

			if kx == ky {
				t.Errorf("two platforms differing in %s share Κₜ"+
					"\n  an amd64 result would answer an arm64 lookup", c.name)
			}
		})
	}
}
