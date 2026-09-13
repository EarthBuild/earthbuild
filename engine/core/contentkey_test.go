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

// An element with no content keeps its own identity.
//
// **A stack holds declarations as well as trees** (§3.2a), and only a tree has a
// manifest to fold. A declaration's identity is over its content already, as is
// a layer pulled by digest, so neither carries a clock and neither needs one
// taken out - refusing them instead made Κₜ underivable for very nearly every
// step, every base over an image carrying a declaration.
//
// The fallback is the identity and not a placeholder, so two elements that
// differ still differ: it can cost a hit and never cause one.
func TestAnElementWithNoContentKeepsItsIdentity(t *testing.T) {
	t.Parallel()

	n := execOver(&ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64})

	// A store that knows about neither.
	blobs := knownContent{}

	first, ok := core.DeriveContentKey(n, []ir.NodeID{digest(1)}, nil, blobs)
	if !ok {
		t.Fatal("a base of elements with no content was refused, so every base" +
			" over an image is underivable")
	}

	second, _ := core.DeriveContentKey(n, []ir.NodeID{digest(2)}, nil, blobs)

	if first == second {
		t.Error("two different elements with no content shared a key, which is" +
			" the collapse a placeholder would cause")
	}
}

// A store that cannot answer at all derives nothing.
//
// Distinct from an element it has nothing to say about: a store with no content
// to give would key every base on its layer ids, which is Κ₁ under another
// domain and a second entry published for nothing.
func TestAStoreThatCannotAnswerDerivesNoContentKey(t *testing.T) {
	t.Parallel()

	n := execOver(&ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64})

	if _, ok := core.DeriveContentKey(n, []ir.NodeID{digest(1)}, nil, allBlobs{}); ok {
		t.Error("a store with no content to give derived a key anyway")
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
