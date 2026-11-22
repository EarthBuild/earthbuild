package guest

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Every field of a request survives the wire.
//
// A field added to `Request` and not to the JSON is silent in the worst way: the
// step runs, the build is correct, and the thing the field asked for simply does
// not happen. `Trace` is exactly that shape - drop it and no RUN is ever
// observed, so no RUN ever earns an L2 hit, and nothing fails.
//
// The engine already has this failure recorded once: a stale `earth-guestd`
// ignoring a field a newer client sends. That is the same defect from the other
// side, and the protocol's `Version` is unread, so nothing catches it.
//
// Reflective rather than a list, on the same argument as the key-coverage guard:
// a hand-kept list of fields to check is a second place to forget the field. A
// non-zero value is set through reflection for every exported field, the whole
// thing is round-tripped, and what comes back must equal what went in.
func TestEveryRequestFieldSurvivesTheWire(t *testing.T) {
	t.Parallel()

	var req Request

	v := reflect.ValueOf(&req).Elem()
	rt := v.Type()

	for i := range rt.NumField() {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}

		// A tag of "-" would be a deliberate exclusion. There are none today,
		// and one added later should say so here rather than pass quietly.
		if tag := f.Tag.Get("json"); strings.HasPrefix(tag, "-") {
			t.Errorf("%s is excluded from the wire; if that is intended, say"+
				" why here", f.Name)

			continue
		}

		if !fill(v.Field(i)) {
			t.Errorf("%s is a %s, which this guard does not know how to fill;"+
				" teach it, rather than leaving the field unchecked",
				f.Name, f.Type)
		}
	}

	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	var back Request

	err = json.Unmarshal(raw, &back)
	if err != nil {
		t.Fatal(err)
	}

	for i := range rt.NumField() {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}

		sent, got := v.Field(i).Interface(), reflect.ValueOf(back).Field(i).Interface()
		if !reflect.DeepEqual(sent, got) {
			t.Errorf("%s did not survive the wire: sent %v, got %v"+
				"\n  a missing json tag, or a name the other side does not read",
				f.Name, sent, got)
		}
	}
}

// fill puts a distinguishable non-zero value in a field.
//
// Non-zero matters: `omitempty` is on most of these, so a zero value is not
// written at all and a round trip of one proves nothing.
func fill(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(true)
	case reflect.String:
		v.SetString("x")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(7)
	// Uint8 among them so that a `[]byte` field is filled through the slice
	// case above rather than reported as unknown - which is what a request
	// carrying an image's configuration is.
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(7)
	case reflect.Slice:
		e := reflect.New(v.Type().Elem()).Elem()
		if !fill(e) {
			return false
		}

		v.Set(reflect.Append(v, e))
	case reflect.Map:
		k, e := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
		if !fill(k) || !fill(e) {
			return false
		}

		v.Set(reflect.MakeMap(v.Type()))
		v.SetMapIndex(k, e)
	case reflect.Pointer:
		// An optional field: nil means "not asked for", so the round trip has to
		// carry a pointer to something filled rather than a nil the encoder
		// would omit and the guard would then be proving nothing about.
		v.Set(reflect.New(v.Type().Elem()))

		return fill(v.Elem())
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() && !fill(v.Field(i)) {
				return false
			}
		}
	default:
		return false
	}

	return true
}
