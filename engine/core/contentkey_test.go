package core_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

func execOver(base *ir.Node) *ir.Node {
	return &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{"cc", testSource}},
		Inputs: []*ir.Node{base}, Platform: amd64,
	}
}

// A stack whose layers differ but whose tree does not derives one key.
//
// **The case Κ₁ cannot express.** A layer's identity hashes mtimes (I8), so a
// deterministic step rebuilt after an eviction has a different id and every key
// above it misses although nothing observable changed. Measured on two cold
// builds of examples/rust-layered: fourteen of the eighteen results carrying a
// delta agreed about their content and disagreed about their id; on a
// Substrate-family target, four of thirty-seven steps.
//
// Named by the tree rather than by the layers, so the same holds for a stack Φ
// flattened - see TestTwoStacksOneTreeDeriveOneKey.
func TestTheContentKeyIsUnmovedByATimestamp(t *testing.T) {
	t.Parallel()

	tree := digest(77)

	first := []ir.NodeID{digest(1)}
	second := []ir.NodeID{digest(2)} // the same tree, rebuilt

	blobs := knownTrees{first[0].String(): tree, second[0].String(): tree}

	if keyOf(t, first, blobs) != keyOf(t, second, blobs) {
		t.Error("a rebuilt but identical base derived a different key," +
			"\n  which is the miss this tier exists to remove")
	}

	// And Κ₁ still tells them apart, which is why the tier is needed at all.
	n := execOver(&ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64})
	if core.DeriveChainKey(n, first, nil) == core.DeriveChainKey(n, second, nil) {
		t.Error("the chain key did not distinguish two ids, so this proves nothing")
	}
}

// The content key never collides with the chain key.
//
// Both hash the same operation, environment and platform; only the domain byte
// and the identity of the base separate them. A collision would let an entry
// published under one be served under the other.
func TestTheContentKeyIsSeparatedFromTheChainKey(t *testing.T) {
	t.Parallel()

	// The degenerate case: a stack whose tree digest equals its own single
	// layer id, so the two derivations differ in nothing but the domain byte.
	same := digest(1)
	blobs := knownTrees{same.String(): same}

	n := execOver(&ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64})

	if keyOf(t, []ir.NodeID{same}, blobs) == core.DeriveChainKey(n, []ir.NodeID{same}, nil) {
		t.Error("the content key collided with the chain key over identical inputs")
	}
}
