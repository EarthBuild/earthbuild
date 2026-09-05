package ir_test

import (
	"reflect"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/internal/vary"
)

// Every field of an operation reaches the node's identity.
//
// `engine/core` has had this guard for the *chain key* since Op.Content was
// keyed and not identified. Identity had none, so the mirror ran the other way
// unwatched: two fields were added to `ir.Mount`, hashed into the key by a guard
// that insisted on it, and hashed into identity only because the same hand
// happened to write both (E432).
//
// Identity is what deduplicates nodes in a graph. A field that changes what a
// step does and does not change its identity makes two different steps one node,
// and the survivor's operation is whichever was built first - a wrong build with
// no cache involved at all.
func TestEveryOperationFieldReachesTheIdentity(t *testing.T) {
	t.Parallel()

	walk(t, reflect.TypeFor[ir.Op](), func(op reflect.Value) ir.NodeID {
		//nolint:forcetypeassert // constructed from ir.Op
		return (&ir.Node{Op: op.Interface().(ir.Op)}).ID()
	})
}

// Every field of a mount reaches the node's identity, for the same reason one
// level down: `Mounts` is one field of `ir.Op`, so varying it proves the slice
// is identified and says nothing about the element.
func TestEveryMountFieldReachesTheIdentity(t *testing.T) {
	t.Parallel()

	walk(t, reflect.TypeFor[ir.Mount](), func(m reflect.Value) ir.NodeID {
		op := ir.Op{Kind: ir.OpExec, Args: []string{"make"}}
		//nolint:forcetypeassert // constructed from ir.Mount
		op.Mounts = []ir.Mount{m.Interface().(ir.Mount)}

		return (&ir.Node{Op: op}).ID()
	})
}

// walk varies each field of typ in turn and demands that id changes.
func walk(t *testing.T, typ reflect.Type, id func(reflect.Value) ir.NodeID) {
	t.Helper()

	for i := range typ.NumField() {
		f := typ.Field(i)

		t.Run(f.Name, func(t *testing.T) {
			t.Parallel()

			var ids [2]ir.NodeID

			for which := range 2 {
				v := reflect.New(typ).Elem()
				if !vary.Value(v.Field(i), which) {
					t.Fatalf("this guard does not know how to vary %s (%s), so it"+
						" is not covering it", f.Name, f.Type)
				}

				ids[which] = id(v)
			}

			if ids[0] == ids[1] {
				t.Errorf("changing %s.%s does not change the node's identity"+
					"\n  two steps differing only in it would be one node in the graph",
					typ.Name(), f.Name)
			}
		})
	}
}
