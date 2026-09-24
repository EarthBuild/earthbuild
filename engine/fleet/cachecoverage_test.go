package fleet

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/internal/vary"
)

// Every field of a shared cache survives the wire.
//
// `TestEveryOpFieldSurvivesTheWire` one level up varies `Op.Caches` as a slice,
// which proves the slice is carried and says nothing about the element - the
// same blind spot `TestEveryMountFieldReachesTheIdentity` was written for in
// `engine/ir`, where varying `Op.Mounts` left every field of a mount unwatched.
//
// The consequence here is the worse one. A cache field written and not read
// shifts every field after it, so a worker's `Helper` becomes its
// `PortableExcept` and the step runs against a claim its author never made.
func TestEveryCacheFieldSurvivesTheWire(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeFor[Cache]()

	for i := range typ.NumField() {
		f := typ.Field(i)

		t.Run(f.Name, func(t *testing.T) {
			t.Parallel()

			c := reflect.New(typ).Elem()
			if !vary.Value(c.Field(i), 1) {
				t.Fatalf("this guard does not know how to vary %s (%s), so it is"+
					" not covering it", f.Name, f.Type)
			}

			//nolint:forcetypeassert // constructed from Cache
			want := Op{Kind: KindExec, Caches: []Cache{c.Interface().(Cache)}}

			got, err := Decode(Encode(Assignment{Version: Version, Op: want}))
			if err != nil {
				t.Fatalf("decoding what we encoded: %v", err)
			}

			// The field, not the encoding - see the note in opcoverage_test.go.
			// Two encodings agree about a field neither side carries.
			if len(got.Op.Caches) != 1 {
				t.Fatalf("one cache went out and %d came back", len(got.Op.Caches))
			}

			sent := fmt.Sprintf("%v", c.Field(i).Interface())
			back := fmt.Sprintf("%v", reflect.ValueOf(got.Op.Caches[0]).Field(i).Interface())

			if sent != back {
				t.Errorf("Cache.%s did not survive the wire: sent %s, back %s"+
					"\n  a field in one of encodeOp/decoder.op and not the other"+
					" shifts every field after it; one in neither crosses nothing"+
					" at all", f.Name, sent, back)
			}
		})
	}
}

// And every field of a shared cache reaches the mount the worker builds.
//
// Surviving the wire is half of it. `operationOf` is a third hand-written list
// over the same struct, and a field that arrives and is dropped there is a
// worker running the step against a declaration the driver never sent - which
// is E433's failure exactly, with the field name changed.
//
// `Target` is exempt from the digest comparison only in the sense that it is
// covered like the rest; nothing here knows which field is which, which is the
// point.
func TestEveryCacheFieldReachesTheMount(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeFor[Cache]()

	for i := range typ.NumField() {
		f := typ.Field(i)

		t.Run(f.Name, func(t *testing.T) {
			t.Parallel()

			var mounts [2]string

			for which := range 2 {
				c := reflect.New(typ).Elem()
				if !vary.Value(c.Field(i), which) {
					t.Fatalf("this guard does not know how to vary %s (%s), so it"+
						" is not covering it", f.Name, f.Type)
				}

				//nolint:forcetypeassert // constructed from Cache
				op, err := operationOf(Op{
					Kind: KindExec, Caches: []Cache{c.Interface().(Cache)},
				})
				if err != nil {
					t.Fatalf("rebuilding the operation: %v", err)
				}

				mounts[which] = spell(op.Mounts)
			}

			if mounts[0] == mounts[1] {
				t.Errorf("changing Cache.%s changes nothing about the mount the"+
					" worker builds\n  got %s"+
					"\n  the field crosses the wire and is dropped rebuilding the"+
					" operation, so the worker runs a declaration nobody sent",
					f.Name, mounts[0])
			}
		})
	}
}

func spell(ms []ir.Mount) string { return fmt.Sprintf("%#v", ms) }
