package fleet_test

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
)

func session() fleet.Session {
	return fleet.Session{
		Session: "s", RunID: "1234", Attempt: 1, Repo: "github.com/org/repo",
	}
}

// A key cannot be derived from public metadata alone.
//
// C.1 makes the `secret` term normative, and the reason is the whole of the
// fleet's security: a run identifier is visible on a public repository, so a key
// derived without a secret can be derived by **any observer**, who then joins
// the mesh and serves results into somebody's build.
//
// Refused in the derivation rather than checked by callers, because there is no
// honest reason to want the weaker key and a check somebody must remember is one
// somebody will not.
func TestAKeyCannotBeDerivedFromPublicMetadataAlone(t *testing.T) {
	t.Parallel()

	for _, secret := range [][]byte{nil, {}} {
		_, err := fleet.DeriveDriverKey(session(), secret)
		if !errors.Is(err, fleet.ErrNoSecret) {
			t.Errorf("a key was derived with secret %v: %v"+
				"\n  anyone watching the repository could derive the same one",
				secret, err)
		}
	}
}

// The same session and secret give the same key, and nothing else does.
//
// Two properties in one table, because they are the same property from two
// sides: the key must be reproducible by the driver's own workers and
// unreachable from any neighbouring session.
func TestEveryTermChangesTheKey(t *testing.T) {
	t.Parallel()

	secret := []byte("shh")

	base, err := fleet.DeriveDriverKey(session(), secret)
	if err != nil {
		t.Fatal(err)
	}

	again, err := fleet.DeriveDriverKey(session(), secret)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(base, again) {
		t.Fatal("the same session and secret derived two different keys;" +
			" no worker could reproduce the driver's identity")
	}

	for _, tc := range []struct {
		name string
		s    fleet.Session
		sec  []byte
	}{
		{"the session", fleet.Session{Session: "t", RunID: "1234", Attempt: 1, Repo: "github.com/org/repo"}, secret},
		{"the run", fleet.Session{Session: "s", RunID: "1235", Attempt: 1, Repo: "github.com/org/repo"}, secret},
		{"the attempt", fleet.Session{Session: "s", RunID: "1234", Attempt: 2, Repo: "github.com/org/repo"}, secret},
		{"the repository", fleet.Session{Session: "s", RunID: "1234", Attempt: 1, Repo: "github.com/org/other"}, secret},
		{"the secret", session(), []byte("shh!")},
	} {
		other, err := fleet.DeriveDriverKey(tc.s, tc.sec)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}

		if bytes.Equal(base, other) {
			t.Errorf("changing %s did not change the key; two distinct"+
				" sessions would share one mesh", tc.name)
		}
	}
}

// The terms cannot be re-split into a different session.
//
// `session ‖ run_id ‖ attempt ‖ repo` over raw strings lets ("ab", "c") and
// ("a", "bc") derive the **same key**, so two different builds would share a
// mesh and each could serve the other results. The concatenation is the
// canonical encoding for exactly the reason it is in a cache key: a
// non-injective encoding maps two distinct things to one identity (§1.4).
//
// This is the case that would never be found by inspection - the derivation
// looks right either way, and the collision needs somebody to try it.
func TestTheTermsCannotBeReSplit(t *testing.T) {
	t.Parallel()

	secret := []byte("shh")

	a, err := fleet.DeriveDriverKey(fleet.Session{Session: "ab", RunID: "c"}, secret)
	if err != nil {
		t.Fatal(err)
	}

	b, err := fleet.DeriveDriverKey(fleet.Session{Session: "a", RunID: "bc"}, secret)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(a, b) {
		t.Error(`("ab","c") and ("a","bc") derived the same key;` +
			" the terms are concatenated without length prefixes and two" +
			" distinct sessions share a mesh")
	}
}

// Deriving the key is necessary and not sufficient.
//
// A secret can leak, and an allowlist can be narrowed without rotating one -
// which is why C.1 has both. An empty allowlist admits **nobody**, the opposite
// of the usual convention that an empty filter matches everything: a driver that
// forgot to publish one talks to no worker rather than to any.
func TestAnAllowlistRefusesWhoeverIsNotOnIt(t *testing.T) {
	t.Parallel()

	known, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	stranger, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	list := fleet.NewAllowlist(known)

	if !list.Allows(known) {
		t.Error("a published worker was refused")
	}

	if list.Allows(stranger) {
		t.Error("a worker nobody published was admitted; deriving the key is" +
			" necessary and must not be sufficient")
	}

	if fleet.NewAllowlist().Allows(known) {
		t.Error("an empty allowlist admitted somebody; a driver that forgot to" +
			" publish one must talk to no worker rather than to any")
	}

	if list.Allows(nil) {
		t.Error("an empty identity was admitted")
	}
}
