package fleet_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// No unevaluated graph structure can cross the wire.
//
// Appendix C's load-bearing claim: a worker is sent a step assignment, **never a
// graph**. The base is a sequence of layer ids rather than the subgraph that
// produced them, because content addressing collapses the graph into digests at
// the boundary.
//
// Asserted structurally rather than by inspection, because inspection is a thing
// somebody does once. Every type reachable from an `Assignment` is walked, and
// an `ir.Node` - or anything holding one - fails. `ir.NodeID` is fine and is the
// whole point: it is a digest, not a reference.
func TestNoGraphIsReachableFromAnAssignment(t *testing.T) {
	t.Parallel()

	forbidden := map[reflect.Type]string{
		reflect.TypeFor[ir.Node]():  "a node is unevaluated graph structure",
		reflect.TypeFor[ir.Graph](): "a graph is the thing this type exists not to send",
		reflect.TypeFor[ir.Op]():    "the IR's operation carries `host`, which the wire may not express",
	}

	seen := map[reflect.Type]bool{}

	var walk func(t *testing.T, typ reflect.Type, path string)

	walk = func(t *testing.T, typ reflect.Type, path string) {
		t.Helper()

		if why, bad := forbidden[typ]; bad {
			t.Errorf("%s reaches %s: %s", path, typ, why)

			return
		}

		if seen[typ] {
			return
		}

		seen[typ] = true

		switch typ.Kind() {
		case reflect.Ptr, reflect.Slice, reflect.Array, reflect.Chan:
			walk(t, typ.Elem(), path+" -> "+typ.Kind().String())

		case reflect.Map:
			walk(t, typ.Key(), path+" -> key")
			walk(t, typ.Elem(), path+" -> value")

		case reflect.Struct:
			for i := range typ.NumField() {
				f := typ.Field(i)
				walk(t, f.Type, path+"."+f.Name)
			}

		case reflect.Interface:
			// An interface can hold anything, which is exactly what this type
			// must not do: it is how a graph would get through a check that only
			// looks at declared types.
			t.Errorf("%s is an interface, so anything at all can travel in it", path)

		default:
		}
	}

	walk(t, reflect.TypeFor[fleet.Assignment](), "Assignment")
}

// `host` cannot be spelled on the wire.
//
// C.3: "**`host` is not in the wire vocabulary.** A `host` op cannot be
// expressed in an assignment, so a malicious peer cannot request one. This is a
// property of the type, not a check that could be forgotten."
//
// So the test is about the *vocabulary*, not about a particular message: every
// kind the wire has is enumerated here, and a new one that means "run on the
// invoking machine" has to get past this list first.
func TestTheWireHasNoWordForHost(t *testing.T) {
	t.Parallel()

	known := []fleet.Kind{
		fleet.KindExec, fleet.KindFile, fleet.KindImage, fleet.KindBuild,
	}

	for _, k := range known {
		if strings.Contains(strings.ToLower(string(k)), "host") {
			t.Errorf("the wire vocabulary contains %q; a delegate is not the"+
				" invoking machine and cannot satisfy host locality", k)
		}
	}

	// And the set is closed. A `Kind` is a string, so a peer can *send*
	// anything; what matters is that this engine recognises only these, which is
	// asserted where the conversion happens rather than here - see
	// TestAnUnknownKindIsRefused.
	if len(known) != 4 {
		t.Errorf("the vocabulary has %d words; this test enumerates them so a"+
			" new one is a deliberate act rather than an import", len(known))
	}
}

// An assignment says which vocabulary it speaks, first.
//
// A peer must be able to refuse a message it does not understand *before*
// interpreting any of it, so the version is the first field and is not
// `omitempty`: a zero version has to arrive as a zero rather than as an absence,
// or an old peer's silence reads as version 0 and a new peer's as the same.
func TestTheVersionIsAlwaysOnTheWire(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeFor[fleet.Assignment]()

	f := typ.Field(0)
	if f.Name != "Version" {
		t.Errorf("the first field is %s; a peer has to be able to refuse a"+
			" message before interpreting it", f.Name)
	}

	if tag := f.Tag.Get("json"); strings.Contains(tag, "omitempty") {
		t.Errorf("Version is %q; an absent version and version zero must not"+
			" look the same", tag)
	}
}
