package core_test

import (
	"reflect"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/internal/vary"
)

// derivations are the three functions that turn an operation into a key.
//
// Κ₁ keys a step on its whole base; Κ₂ keys it on what it read; the class is
// what a profile is stored under, and decides *which* observation is used to
// predict a step's reads. All three answer "is this the same operation", and
// all three were maintained separately.
func derivations() []struct {
	name string
	of   func(*ir.Node) core.Key
} {
	return []struct {
		name string
		of   func(*ir.Node) core.Key
	}{
		{"the chain key", func(n *ir.Node) core.Key { return core.DeriveChainKey(n, nil, nil) }},
		{"the observed key", func(n *ir.Node) core.Key {
			return core.DeriveObservedKey(n, nil, core.Observation{Reads: map[string]ir.NodeID{"/x": {1}}})
		}},
		{"the step class", core.StepClass},
	}
}

// Every field of an operation reaches every key derived from it.
//
// `TestEveryOperationFieldReachesTheKey` has guarded Κ₁ since `Op.Content` was
// added to node identity and not to the key, produced four false cache hits and
// reached a real build. Its own comment says a written reminder "would be a
// comment" and that this is a test instead.
//
// It was applied to one of three derivations. Κ₂ and `StepClass` hash the kind,
// the arguments, the environment and the platform - and none of `Dir`, `User`,
// `NoCache`, `Docker`, `Entrypoint`, `DirCopy`, `NoFollow`, `KeepOwn` or
// `Tolerate`, two of which this branch added.
//
// What that costs, concretely:
//
//	RUN --user root  install …     same class, same Κ₂ as
//	RUN --user build install …
//
// Both derive one profile and one observed key. If the observation matches -
// and it would, since the reads are identical - L2 serves the root build's
// layer for the unprivileged one. I3, and it is silent: the build succeeds and
// the image has files owned by the wrong user.
//
// **The failure class, fifth instance, and the sharpest.** Not a rule somebody
// forgot to write down: a rule written down *as an executable guard*, with a
// comment explaining that comments do not work, applied to one of the three
// places it holds.
func TestEveryOperationFieldReachesEveryKey(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeFor[ir.Op]()

	for _, d := range derivations() {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()

			for i := range typ.NumField() {
				f := typ.Field(i)

				t.Run(f.Name, func(t *testing.T) {
					t.Parallel()

					keys := make([]core.Key, 2)

					for which := range 2 {
						op := reflect.New(typ).Elem()

						if !vary.Value(op.Field(i), which) {
							t.Fatalf("this guard cannot vary %s (%s), so it is not covering it"+
								"\n  teach vary.Value() about the type", f.Name, f.Type)
						}

						n := &ir.Node{Op: op.Interface().(ir.Op)} //nolint:forcetypeassert // from ir.Op
						keys[which] = d.of(n)
					}

					if keys[0] == keys[1] {
						t.Errorf("changing Op.%s does not change %s"+
							"\n  two operations that differ share it, so one's result"+
							" can be served for the other", f.Name, d.name)
					}
				})
			}
		})
	}
}

// A source a step reads reaches the observed key too.
//
// `refs` are the inputs a step reads but does not stand on - a `COPY --from`
// source, a local context. They are not fields of `ir.Op`, so the guard above
// cannot reach them, and they are the one thing Κ₁ hashes that Κ₂ deliberately
// might not have.
//
// It must. Κ₂ says "this result is valid wherever this step observed these
// paths"; a COPY whose *source layer* changed produces different bytes having
// observed the same path, so a Κ₂ that ignored refs would serve the old file.
//
// The class is the deliberate exception and is tested for the opposite: a
// prediction key that moved whenever a source file changed would have no
// history to predict from, and safety does not rest on it because `tryL2`
// derives the exact Κ₂ before serving anything.
func TestASourceReachesTheObservedKeyButNotTheClass(t *testing.T) {
	t.Parallel()

	n := &ir.Node{Op: ir.Op{Kind: ir.OpFile, Args: []string{testCopySrc, testCopyDst}}}
	obs := core.Observation{Reads: map[string]ir.NodeID{testCopySrc: {7}}}

	a := core.DeriveObservedKey(n, []ir.NodeID{{1}}, obs)
	b := core.DeriveObservedKey(n, []ir.NodeID{{2}}, obs)

	if a == b {
		t.Error("a COPY from a changed source derived the same observed key," +
			" so the previous file would be served for the new one")
	}

	// Two *equal* nodes, not one node twice. Comparing `StepClass(n)` with
	// itself can only fail through hidden state inside the call, and says
	// nothing about the claim worth making - that a class is a function of the
	// node's content, so a second node built the same way lands in the same
	// class. A map iterated during construction would fail this and pass the
	// other (SA4000, found by the linter reaching this code for the first time).
	same := &ir.Node{Op: ir.Op{Kind: ir.OpFile, Args: []string{testCopySrc, testCopyDst}}}
	if core.StepClass(n) != core.StepClass(same) {
		t.Error("two nodes with the same content are in different classes," +
			" so a class is not a function of what the step is")
	}
}
