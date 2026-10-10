package core_test

import (
	"reflect"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/internal/vary"
)

// TestEveryOperationFieldReachesTheKey walks ir.Op and fails if any field can
// change without changing the chain key.
//
// It exists because of a bug that reached a real build: Op.Content was added to
// node *identity* and not to the key, so editing a copied source file produced
// four cache hits and the previous output. Identity and key are derived by
// different functions over the same struct, and nothing connected them.
//
// A guard written as "remember to update DeriveChainKey" would be a comment.
// This is a test: adding a field to ir.Op without keying on it fails here,
// naming the field.
func TestEveryOperationFieldReachesTheKey(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeFor[ir.Op]()

	for i := range typ.NumField() {
		f := typ.Field(i)

		t.Run(f.Name, func(t *testing.T) {
			t.Parallel()
			keys := make([]core.Key, 2)

			for which := range 2 {
				op := reflect.New(typ).Elem()

				if !vary.Value(op.Field(i), which) {
					t.Fatalf("this guard does not know how to vary %s (%s), so it is not covering it"+
						"\n  teach vary.Value() about the type, or the field is unprotected", f.Name, f.Type)
				}

				n := &ir.Node{Op: op.Interface().(ir.Op)} //nolint:forcetypeassert // constructed from ir.Op
				keys[which] = core.DeriveChainKey(n, nil, nil)
			}

			if keys[0] == keys[1] {
				t.Errorf("changing Op.%s does not change the chain key"+
					"\n  a step whose result depends on it would hit the cache after it changed", f.Name)
			}
		})
	}
}

// The same guard for node identity, which is what the graph is compared by.
func TestEveryOperationFieldReachesNodeIdentity(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeFor[ir.Op]()

	for i := range typ.NumField() {
		f := typ.Field(i)

		t.Run(f.Name, func(t *testing.T) {
			t.Parallel()
			ids := make([]ir.NodeID, 2)

			for which := range 2 {
				op := reflect.New(typ).Elem()

				if !vary.Value(op.Field(i), which) {
					t.Fatalf("this guard cannot vary %s (%s)", f.Name, f.Type)
				}

				n := &ir.Node{Op: op.Interface().(ir.Op)} //nolint:forcetypeassert // constructed from ir.Op
				ids[which] = n.ID()
			}

			if ids[0] == ids[1] {
				t.Errorf("changing Op.%s does not change the node's identity", f.Name)
			}
		})
	}
}

// Platform decides which image is pulled and which binaries run, so it belongs
// in the key by the same argument.
func TestPlatformReachesTheKey(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeFor[ir.Platform]()

	for i := range typ.NumField() {
		f := typ.Field(i)

		t.Run(f.Name, func(t *testing.T) {
			t.Parallel()
			keys := make([]core.Key, 2)

			for which := range 2 {
				p := reflect.New(typ).Elem()

				if !vary.Value(p.Field(i), which) {
					t.Fatalf("this guard cannot vary %s (%s)", f.Name, f.Type)
				}

				n := &ir.Node{Platform: p.Interface().(ir.Platform)} //nolint:forcetypeassert // constructed
				keys[which] = core.DeriveChainKey(n, nil, nil)
			}

			if keys[0] == keys[1] {
				t.Errorf("changing Platform.%s does not change the chain key", f.Name)
			}
		})
	}
}

// The same guard for the observed-input key.
//
// Κ₂ claims "this step reads exactly these things", so a component of an
// observation that does not reach the key makes that claim about something it
// did not check - and L2 hits are the ones taken across *different* bases, where
// a false hit is hardest to notice.
func TestEveryObservationFieldReachesTheObservedKey(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeFor[core.Observation]()

	for i := range typ.NumField() {
		f := typ.Field(i)

		// Incomplete is not keyed and must not be: it is a statement about the
		// observation's own quality, and the scheduler refuses to derive a key
		// at all when it is set (see TestIncompleteObservationsAreNotKeyed).
		// Keying on it would make a complete and an incomplete observation of
		// the same reads into different steps.
		if f.Name == "Incomplete" {
			continue
		}

		// Why is not keyed for the same reason and one more: it is a
		// *diagnostic*, and keying on it would make two machines whose tracers
		// failed with different errnos into different steps - having observed
		// the same reads.
		if f.Name == "Why" {
			continue
		}

		t.Run(f.Name, func(t *testing.T) {
			t.Parallel()
			keys := make([]core.Key, 2)

			for which := range 2 {
				obs := reflect.New(typ).Elem()

				if !vary.Value(obs.Field(i), which) {
					t.Fatalf("this guard cannot vary %s (%s)", f.Name, f.Type)
				}

				n := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"x"}}}
				//nolint:forcetypeassert // constructed
				keys[which] = core.DeriveObservedKey(n, nil, obs.Interface().(core.Observation))
			}

			if keys[0] == keys[1] {
				t.Errorf("changing Observation.%s does not change the observed-input key"+
					"\n  a step would hit across a base that differs in exactly this", f.Name)
			}
		})
	}
}
