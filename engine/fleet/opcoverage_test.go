package fleet

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/EarthBuild/earthbuild/internal/vary"
)

// Every field of a wire operation survives the wire.
//
// `encodeOp` and `decoder.op()` are two hand-written lists over one struct, kept
// in step by nobody - the arrangement that had a mount field hashed into one of
// two digests and not the other (E432). Here the consequence is worse than a
// cache miss: a field written and not read shifts every field after it, so the
// worker's `User` becomes its `Dir` and the step runs somewhere else as somebody
// else. A field read and not written does the same in reverse.
//
// Varying rather than round-tripping one fixed value: a zero field survives any
// codec at all, including one that never mentions it.
func TestEveryOpFieldSurvivesTheWire(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeFor[Op]()

	for i := range typ.NumField() {
		f := typ.Field(i)

		t.Run(f.Name, func(t *testing.T) {
			t.Parallel()

			v := reflect.New(typ).Elem()
			v.FieldByName("Kind").SetString(string(KindExec))

			if f.Name != "Kind" && !vary.Value(v.Field(i), 1) {
				t.Fatalf("this guard does not know how to vary %s (%s), so it is"+
					" not covering it", f.Name, f.Type)
			}

			//nolint:forcetypeassert // constructed from Op
			want := v.Interface().(Op)

			got, err := Decode(Encode(Assignment{Version: Version, Op: want}))
			if err != nil {
				t.Fatalf("decoding what we encoded: %v", err)
			}

			// Compared by re-encoding rather than by DeepEqual: a decoder
			// returns an empty slice where the sender had nil, and the wire has
			// no way to express that difference - so demanding it is demanding
			// something the format cannot carry. What must hold is that what
			// came back says the same thing.
			if !bytes.Equal(
				Encode(Assignment{Version: Version, Op: got.Op}),
				Encode(Assignment{Version: Version, Op: want}),
			) {
				t.Errorf("Op.%s did not survive the wire"+
					"\n  sent %#v\n  back %#v"+
					"\n  a field in one of encodeOp/decoder.op and not the other"+
					" shifts every field after it", f.Name, want, got.Op)
			}
		})
	}
}

// A field that changes the operation changes its bytes.
//
// Survival is not enough on its own: a codec could carry a field faithfully and
// a *key* derived from these bytes could still ignore it, which is how two
// different steps get one identity on the wire.
func TestEveryOpFieldChangesTheEncoding(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeFor[Op]()

	for i := range typ.NumField() {
		f := typ.Field(i)

		t.Run(f.Name, func(t *testing.T) {
			t.Parallel()

			var enc [2][]byte

			for which := range 2 {
				v := reflect.New(typ).Elem()
				v.FieldByName("Kind").SetString(string(KindExec))

				if f.Name == "Kind" {
					v.FieldByName("Kind").SetString(
						[]string{string(KindExec), string(KindFile)}[which])
				} else if !vary.Value(v.Field(i), which) {
					t.Fatalf("this guard cannot vary %s (%s)", f.Name, f.Type)
				}

				//nolint:forcetypeassert // constructed from Op
				enc[which] = Encode(Assignment{Version: Version, Op: v.Interface().(Op)})
			}

			if bytes.Equal(enc[0], enc[1]) {
				t.Errorf("changing Op.%s does not change the encoded assignment"+
					"\n  two different steps would be one step on the wire", f.Name)
			}
		})
	}
}
