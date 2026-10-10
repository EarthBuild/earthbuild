package fleet

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/EarthBuild/earthbuild/internal/vary"
)

// Every field of a hint survives the wire.
//
// The third of these guards, and it found what the first two were written for.
// `Hints` is four hand-written lists over one struct - the encoder, the decoder,
// the JSON tags, and the struct itself - and nothing held them together, so a
// field could be set by the driver, documented as crossing, tagged
// `json:"bytes,omitempty"`, and dropped by the binary codec that actually
// carries it.
//
// That is `Hints.Bytes`, and it was found by writing this rather than by
// reading. It is used only on the driver today, so nothing was visibly wrong -
// which is the point: a hint nobody reads is indistinguishable from a hint
// nobody sent, right up until somebody reads it.
func TestEveryHintFieldSurvivesTheWire(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeFor[Hints]()

	for i := range typ.NumField() {
		f := typ.Field(i)

		t.Run(f.Name, func(t *testing.T) {
			t.Parallel()

			h := reflect.New(typ).Elem()
			if !vary.Value(h.Field(i), 1) {
				t.Fatalf("this guard does not know how to vary %s (%s), so it is"+
					" not covering it", f.Name, f.Type)
			}

			//nolint:forcetypeassert // constructed from Hints
			want := Assignment{Version: Version, Op: Op{Kind: KindExec}, Hints: h.Interface().(Hints)}

			got, err := Decode(Encode(want))
			if err != nil {
				t.Fatalf("decoding what we encoded: %v", err)
			}

			// **The field, not the encoding.** Comparing two encodings is what
			// the `Op` and `Cache` guards do, and it is blind in exactly the
			// place that matters: a field neither side carries encodes
			// identically on both, so it round-trips as equal while crossing
			// nothing. That is how `Bytes` passed this guard on the first run.
			//
			// Printed rather than DeepEqual'd, which absorbs the difference the
			// wire genuinely cannot carry: a decoder returns an empty slice
			// where the sender had nil, and both print as `[]`.
			sent := fmt.Sprintf("%v", h.Field(i).Interface())
			back := fmt.Sprintf("%v", reflect.ValueOf(got.Hints).Field(i).Interface())

			if sent != back {
				t.Errorf("Hints.%s did not survive the wire: sent %s, back %s"+
					"\n  a field in one of Encode/Decode and not the other shifts"+
					" every field after it; one in neither crosses nothing at all",
					f.Name, sent, back)
			}
		})
	}
}
