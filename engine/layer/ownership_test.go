package layer_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A round trip is digest-stable on a machine that cannot restore ownership.
//
// **E313, the fault that made two machines unable to share a layer.** A layer's
// identity includes uid and gid (§3.3), and `meta` restores them with an
// attempt whose failure is deliberately ignored - an unprivileged worker cannot
// chown, and refusing there would refuse every honest layer.
//
// The comment on that line says the caller's digest check "catches it instead,
// and says so in the one place that can tell the difference between 'could not'
// and 'did not need to'". **No caller did.** `Layers.Put` captured what landed
// on disk, which on a worker running as somebody else is a different layer, and
// the fleet reported that the peer did not hold what it had just sent.
//
// *Failure class: a field whose documentation describes an intention.*
//
// The stream declares the ownership; what the filesystem accepted is not the
// authority on what the layer *is*. Provoked with a seam rather than described,
// because the real condition needs two users and this must fail on one.
func TestAnUnpackAsAnotherUserStillCapturesTheSameLayer(t *testing.T) { //nolint:paralleltest // see the note above
	// **Not parallel.** The seam it needs is a package variable, and a parallel
	// test that swaps one is a data race against every other test in the
	// package - including the ones that would then be capturing trees with
	// ownership silently unrestored.
	src := t.TempDir()

	err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o600)
	if err != nil {
		t.Fatalf("%v", err)
	}

	err = os.Mkdir(filepath.Join(src, "d"), 0o750)
	if err != nil {
		t.Fatalf("%v", err)
	}

	want, err := layer.Take(src)
	if err != nil {
		t.Fatalf("%v", err)
	}

	var packed bytes.Buffer

	err = layer.Pack(src, &packed)
	if err != nil {
		t.Fatalf("%v", err)
	}

	// Every file lands owned by whoever ran the unpack, as it does when an
	// unprivileged worker restores a layer somebody else packed.
	layer.ObservedOwnerForTest(t, func(uid, gid uint32) (uint32, uint32) {
		return uid + 1, gid + 1
	})

	dst := t.TempDir()

	own, err := layer.UnpackOwned(&packed, dst)
	if err != nil {
		t.Fatalf("%v", err)
	}

	got, err := layer.TakeOwnedIn(dst, layer.IDMap{}, layer.IDMap{}, own)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if got.ID != want.ID {
		t.Errorf("a layer that could not be chowned captured as %v, want %v"+
			"\n  the stream said who owns it and the filesystem was asked"+
			" instead (E313)", got.ID, want.ID)
	}
}

// The ownership a stream declares is reported even when it was applied.
//
// Not a restatement of the test above: that one asserts the digest, which would
// also be right if `UnpackOwned` returned nothing and capture fell back to the
// disk. This asserts the map is populated, so the fallback cannot pass for the
// mechanism.
func TestUnpackReportsTheOwnershipItWasGiven(t *testing.T) {
	t.Parallel()

	src := t.TempDir()

	err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o600)
	if err != nil {
		t.Fatalf("%v", err)
	}

	var packed bytes.Buffer

	err = layer.Pack(src, &packed)
	if err != nil {
		t.Fatalf("%v", err)
	}

	own, err := layer.UnpackOwned(&packed, t.TempDir())
	if err != nil {
		t.Fatalf("%v", err)
	}

	got, ok := own["a.txt"]
	if !ok {
		t.Fatalf("the unpack reported ownership for %v, not a.txt", keysOf(own))
	}

	if got.UID != uint32(os.Getuid()) {
		t.Errorf("a.txt is declared owned by %d, want %d", got.UID, os.Getuid())
	}
}

func keysOf(m map[string]layer.Owner) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}
