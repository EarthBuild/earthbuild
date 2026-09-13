package core_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// knownContent is a 𝔅 that can say a layer's identity without times.
type knownContent map[ir.NodeID]ir.NodeID

func (knownContent) Has(ir.NodeID) bool { return true }

func (k knownContent) ContentOf(id ir.NodeID) (ir.NodeID, bool) {
	c, ok := k[id]

	return c, ok
}

func execOver(base *ir.Node) *ir.Node {
	return &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{"cc", testSource}},
		Inputs: []*ir.Node{base}, Platform: amd64,
	}
}

// Two bases that differ only in their timestamps derive one key.
//
// **The case Κ₁ cannot express.** A layer's identity hashes mtimes (I8), so a
// deterministic step rebuilt after an eviction produces a different id and every
// key above it misses - measured on two cold builds of examples/rust-layered:
// fourteen of the eighteen results that carry a delta agreed about their content
// and disagreed about their id.
func TestTheContentKeyIsUnmovedByATimestamp(t *testing.T) {
	t.Parallel()

	sameContent := digest(99)

	// One tree, captured twice: two ids, one content.
	blobs := knownContent{digest(1): sameContent, digest(2): sameContent}

	n := execOver(&ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64})

	first, ok := core.DeriveContentKey(n, []ir.NodeID{digest(1)}, nil, blobs)
	if !ok {
		t.Fatal("the first base was not derivable")
	}

	second, ok := core.DeriveContentKey(n, []ir.NodeID{digest(2)}, nil, blobs)
	if !ok {
		t.Fatal("the second base was not derivable")
	}

	if first != second {
		t.Error("a rebuilt but identical base derived a different content key," +
			"\n  which is the miss this tier exists to remove")
	}

	// And Κ₁ still tells them apart, which is why the tier is needed at all.
	if core.DeriveChainKey(n, []ir.NodeID{digest(1)}, nil) ==
		core.DeriveChainKey(n, []ir.NodeID{digest(2)}, nil) {
		t.Error("the chain key did not distinguish two ids, so this test proves nothing")
	}
}

// Different content is a different key.
func TestTheContentKeyMovesWithContent(t *testing.T) {
	t.Parallel()

	blobs := knownContent{digest(1): digest(98), digest(2): digest(99)}

	n := execOver(&ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64})

	first, _ := core.DeriveContentKey(n, []ir.NodeID{digest(1)}, nil, blobs)
	second, _ := core.DeriveContentKey(n, []ir.NodeID{digest(2)}, nil, blobs)

	if first == second {
		t.Error("two bases holding different bytes derived one key")
	}
}

// A base whose content nobody can say is not derivable.
//
// **Absent, never assumed.** A store that has no manifest for a layer - an
// older entry, a base fetched as opaque bytes - cannot be given a placeholder
// and keyed anyway: two such bases would then share a key while holding
// anything at all.
func TestAnUnknownContentIsNotDerivable(t *testing.T) {
	t.Parallel()

	n := execOver(&ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64})

	if _, ok := core.DeriveContentKey(n, []ir.NodeID{digest(1)}, nil, knownContent{}); ok {
		t.Error("a base whose content is unknown was keyed anyway")
	}

	// And a store that cannot answer at all is the same answer, not a panic.
	if _, ok := core.DeriveContentKey(n, []ir.NodeID{digest(1)}, nil, allBlobs{}); ok {
		t.Error("a store with no content to give was keyed anyway")
	}
}

// The content key never collides with the chain key.
//
// Both are hashes over the same operation, environment and platform; only the
// domain byte and the identity of the base separate them. A collision would let
// an entry published under one be served under the other.
func TestTheContentKeyIsSeparatedFromTheChainKey(t *testing.T) {
	t.Parallel()

	// The degenerate case: a layer whose content id happens to equal its own
	// id, so the two derivations differ in nothing but their domain byte.
	same := digest(1)
	blobs := knownContent{same: same}

	n := execOver(&ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64})

	content, ok := core.DeriveContentKey(n, []ir.NodeID{same}, nil, blobs)
	if !ok {
		t.Fatal("not derivable")
	}

	if content == core.DeriveChainKey(n, []ir.NodeID{same}, nil) {
		t.Error("the content key collided with the chain key over identical inputs")
	}
}
