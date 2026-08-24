package core_test

import (
	"reflect"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/internal/vary"
)

// Every field of a *mount* reaches the key, not merely the mounts slice.
//
// `TestEveryOperationFieldReachesTheKey` walks `ir.Op` and fills each field with
// two distinguishable values. For `Mounts` it fills the slice - one element,
// varied - which proves the slice reaches the key and proves nothing about the
// element's fields. The hashing of a mount is a hand-written list of five, and
// two fields were added to `ir.Mount` without it noticing (E432).
//
// So this walks the element type. A mount field that changes what a step gets
// and does not change its key is a step served a result produced under different
// conditions - which is the whole failure the Op guard exists to prevent, one
// level down.
func TestEveryMountFieldReachesTheKey(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeFor[ir.Mount]()

	for i := range typ.NumField() {
		f := typ.Field(i)

		t.Run(f.Name, func(t *testing.T) {
			t.Parallel()

			keys := make([]core.Key, 2)

			for which := range 2 {
				m := reflect.New(typ).Elem()

				// A target, always: a mount without one is not a mount, and two
				// mounts differing only in a field nobody set is the comparison
				// this test is about.
				m.FieldByName("Target").SetString("/cache")

				if f.Name != "Target" && !vary.Value(m.Field(i), which) {
					t.Fatalf("this guard does not know how to vary %s (%s), so it is"+
						" not covering it", f.Name, f.Type)
				}

				if f.Name == "Target" {
					m.FieldByName("Target").SetString([]string{"/a", "/b"}[which])
				}

				op := ir.Op{
					Kind: ir.OpExec, Args: []string{"make"},
					// constructed from ir.Mount
					Mounts: []ir.Mount{m.Interface().(ir.Mount)},
				}

				keys[which] = core.DeriveChainKey(&ir.Node{Op: op}, nil, nil)
			}

			if keys[0] == keys[1] {
				t.Errorf("changing Mount.%s does not change the chain key"+
					"\n  a step whose result depends on it would hit the cache after"+
					" it changed", f.Name)
			}
		})
	}
}
