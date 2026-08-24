package decl_test

import (
	"reflect"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/decl"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// rootUser is the user these fixtures declare, named once because a typo in
// one of three copies would read as a different declaration (goconst).
const rootUser = "root"

func full() decl.Declaration {
	return decl.Declaration{
		Env:        []string{"PATH=/go/bin:/usr/local/go/bin", "GOPATH=/go"},
		WorkingDir: "/go",
		User:       rootUser,
		Entrypoint: []string{"/entry"},
		Cmd:        []string{"/bin/sh"},
	}
}

// A declaration round-trips through its canonical form.
func TestADeclarationSurvivesEncoding(t *testing.T) {
	t.Parallel()

	got, err := decl.Decode(decl.Encode(full()))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !reflect.DeepEqual(got, full()) {
		t.Errorf("round-tripped to %+v, want %+v", got, full())
	}
}

// **The order of Env is part of what a declaration says.**
//
// `ENV A=1` then `ENV A=2` is not `ENV A=2` then `ENV A=1`: later wins, so a
// canonical form that sorted them would make two different declarations one, and
// the fold would produce whichever the sort happened to put last (§3.2a).
func TestTheOrderOfEnvIsPartOfTheIdentity(t *testing.T) {
	t.Parallel()

	a := decl.Declaration{Env: []string{"A=1", "A=2"}}
	b := decl.Declaration{Env: []string{"A=2", "A=1"}}

	if decl.ID(a) == decl.ID(b) {
		t.Error("two orderings of the same assignments share an identity")
	}
}

// Every field reaches the identity.
//
// The same guard the operation keys carry, and for the same reason: a field
// added later and left out of the digest makes two different declarations one,
// and the failure is a silent wrong answer rather than an error. Reflection so
// that a new field fails this test rather than waiting to be noticed.
func TestEveryDeclarationFieldReachesTheID(t *testing.T) {
	t.Parallel()

	base := full()
	baseID := decl.ID(base)

	rt := reflect.TypeFor[decl.Declaration]()
	if rt.NumField() == 0 {
		t.Fatal("a declaration has no fields at all, so this checks nothing")
	}

	for i := range rt.NumField() {
		f := rt.Field(i)

		changed := full()
		v := reflect.ValueOf(&changed).Elem().Field(i)

		// Two kinds because a declaration has two, and a third would rather fail
		// here than be varied by a default that changes nothing.
		switch v.Kind() {
		case reflect.String:
			v.SetString("different")
		case reflect.Slice:
			v.Set(reflect.ValueOf([]string{"different"}))
		default:
			t.Fatalf("%s is a %s, which this test does not know how to vary", f.Name, v.Kind())
		}

		if decl.ID(changed) == baseID {
			t.Errorf("changing %s left the identity alone, so it is not in 𝒮(γ)", f.Name)
		}
	}
}

// An empty declaration has an identity too, and it is not a zero digest.
//
// A stack element that declares nothing is still an element: it is the answer
// "this image says nothing", which is different from "nobody asked".
func TestAnEmptyDeclarationHasAnIdentity(t *testing.T) {
	t.Parallel()

	var zero decl.Declaration

	if decl.ID(zero) == (ir.NodeID{}) {
		t.Error("an empty declaration hashes to the zero digest")
	}

	if decl.ID(zero) == decl.ID(full()) {
		t.Error("an empty declaration shares an identity with a full one")
	}
}
