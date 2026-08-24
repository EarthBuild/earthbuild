package fleet_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
)

// A reply carries digests and never bytes.
//
// C.4: blobs move in batches on their own protocol, **verified per chunk**, so a
// peer serving wrong bytes is detected within one chunk rather than at the end
// of a transfer (I2). A result inlined into a control message would be a payload
// nobody chunk-verified, arriving on the stream this engine trusts most.
//
// So the property is structural: no field of a `Reply`, at any depth, is a byte
// slice or a string large enough to be a payload. Digests are fixed-width arrays
// and pass; a `[]byte` does not, whatever it is called.
//
// `ir.NodeID` is an array rather than a slice precisely so this distinction can
// be made by type.
func TestAReplyCarriesNoPayload(t *testing.T) {
	t.Parallel()

	seen := map[reflect.Type]bool{}

	var walk func(t *testing.T, typ reflect.Type, path string)

	walk = func(t *testing.T, typ reflect.Type, path string) {
		t.Helper()

		if seen[typ] {
			return
		}

		seen[typ] = true

		switch typ.Kind() {
		case reflect.Slice:
			if typ.Elem().Kind() == reflect.Uint8 {
				t.Errorf("%s is a byte slice; a result travels on"+
					" earth/blob/1 where every chunk is verified, not inline on"+
					" the control stream", path)

				return
			}

			walk(t, typ.Elem(), path+"[]")

		case reflect.Interface:
			t.Errorf("%s is an interface, so a payload can travel in it"+
				" whatever the declared type says", path)

		case reflect.Pointer, reflect.Chan:
			walk(t, typ.Elem(), path+" -> "+typ.Kind().String())

		case reflect.Map:
			walk(t, typ.Key(), path+" -> key")
			walk(t, typ.Elem(), path+" -> value")

		case reflect.Struct:
			for i := range typ.NumField() {
				f := typ.Field(i)
				walk(t, f.Type, path+"."+f.Name)
			}

		default:
		}
	}

	walk(t, reflect.TypeFor[fleet.Reply](), "Reply")
}

// A reply says which vocabulary it speaks, first and always.
func TestAReplyAlwaysCarriesItsVersion(t *testing.T) {
	t.Parallel()

	f := reflect.TypeFor[fleet.Reply]().Field(0)

	if f.Name != "Version" {
		t.Errorf("the first field of a reply is %s", f.Name)
	}

	if tag := f.Tag.Get("json"); strings.Contains(tag, "omitempty") {
		t.Errorf("Version is %q; an absent version and version zero must not"+
			" look the same", tag)
	}
}

// A refusal and a failed step are different things.
//
// A non-zero exit is a **result**: the step ran and said no, and the build
// should fail with its output. A refusal is a worker declining to run it at all
// - a `host` op it cannot express, a construct it has not implemented - and the
// driver's answer is to run it somewhere else rather than to fail (I10).
//
// Collapsing the two would make a delegate's gap look like the user's error,
// which is the failure mode `ErrRefused` exists to prevent one layer down: a
// refusal reported as a build failure sends somebody to debug their Earthfile.
func TestARefusalIsNotAFailedStep(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeFor[fleet.Reply]()

	exit, okExit := typ.FieldByName("Exit")
	refused, okRefused := typ.FieldByName("Refused")

	if !okExit || !okRefused {
		t.Fatal("a reply must be able to say both that a step failed and that" +
			" it was not run")
	}

	if exit.Type == refused.Type {
		t.Errorf("Exit and Refused are both %s; a step that ran and failed and"+
			" a step nobody ran are different answers and should not be the"+
			" same shape", exit.Type)
	}
}
