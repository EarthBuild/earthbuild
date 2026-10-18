package core_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// knownTrees answers what a stack materialises to, as a store does by folding
// the manifests beside its layers.
type knownTrees map[string]ir.NodeID

func (knownTrees) Has(ir.NodeID) bool { return true }

func (k knownTrees) TreeOf(stack []ir.NodeID) (ir.NodeID, bool) {
	var key string
	for _, id := range stack {
		key += id.String()
	}

	t, ok := k[key]

	return t, ok
}

func keyOf(t *testing.T, stack []ir.NodeID, blobs core.BlobStore) core.Key {
	t.Helper()

	n := execOver(&ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64})

	k, ok := core.DeriveContentKey(n, stack, nil, blobs)
	if !ok {
		t.Fatal("not derivable")
	}

	return k
}

// Two stacks that materialise one tree derive one key.
//
// **What a sequence cannot express.** Κₜ named the base by its layers' content
// ids in order, so a stack Φ had flattened and the stack it flattened were
// different keys although a step sees the same filesystem - and 𝑛ₘₐₓ is
// materialiser-dependent (4.8), so two machines flatten the same target at
// different points. Naming the base by what it folds to closes that.
func TestTwoStacksOneTreeDeriveOneKey(t *testing.T) {
	t.Parallel()

	tree := digest(77)

	long := []ir.NodeID{digest(1), digest(2), digest(3)}
	flat := []ir.NodeID{digest(9), digest(3)} // the oldest two squashed

	blobs := knownTrees{
		long[0].String() + long[1].String() + long[2].String(): tree,
		flat[0].String() + flat[1].String():                    tree,
	}

	if keyOf(t, long, blobs) != keyOf(t, flat, blobs) {
		t.Error("a flattened stack and the stack it flattened derived different keys," +
			"\n  though they materialise one filesystem - which is the whole of" +
			"\n  why the base is named by what it holds rather than how it was made")
	}
}

// And two stacks that materialise different trees still differ.
func TestTwoTreesDeriveTwoKeys(t *testing.T) {
	t.Parallel()

	a := []ir.NodeID{digest(1)}
	b := []ir.NodeID{digest(2)}

	blobs := knownTrees{a[0].String(): digest(77), b[0].String(): digest(78)}

	if keyOf(t, a, blobs) == keyOf(t, b, blobs) {
		t.Error("two bases holding different bytes derived one key")
	}
}

// A store that cannot fold a stack derives nothing.
//
// Absence is an answer, never a guess: keying on the stack's own ids instead
// would be Κ₁ under another domain, a second entry published for nothing.
func TestAStackThatCannotBeFoldedDerivesNoKey(t *testing.T) {
	t.Parallel()

	n := execOver(&ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64})

	if _, ok := core.DeriveContentKey(n, []ir.NodeID{digest(1)}, nil, knownTrees{}); ok {
		t.Error("a stack the store could not fold was keyed anyway")
	}

	if _, ok := core.DeriveContentKey(n, []ir.NodeID{digest(1)}, nil, allBlobs{}); ok {
		t.Error("a store that cannot fold at all derived a key")
	}
}
