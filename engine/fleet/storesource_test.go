package fleet_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/blob"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

func storeWith(t *testing.T, bodies ...string) (*blob.Store, string, []ir.NodeID) {
	t.Helper()

	// The directory is returned rather than asked of the store afterwards: a
	// store does not expose its root, and it should not - a test that needs to
	// reach behind one is doing something the store's API deliberately does not
	// offer, and saying so here is better than adding an accessor for it.
	dir := t.TempDir()

	s, err := blob.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	ids := make([]ir.NodeID, 0, len(bodies))

	for _, b := range bodies {
		id, _, err := s.Put(bytes.NewReader([]byte(b)))
		if err != nil {
			t.Fatal(err)
		}

		ids = append(ids, id)
	}

	return s, dir, ids
}

// A blob moves from one store to another, verified on the way.
//
// The whole of C.4 on one machine: a peer serves from what it holds, the
// receiver verifies each chunk as it arrives, and what lands is byte-identical.
// Two real stores rather than fakes, so the digests are the ones the store
// computes and not the ones a test decided they should be.
func TestABlobMovesBetweenTwoRealStores(t *testing.T) {
	t.Parallel()

	from, _, ids := storeWith(t, "one", "two", "three")

	f := &fleet.Fetch{
		Peers: []fleet.Source{&fleet.StoreSource{Label: "peer", Blobs: from}},
	}

	got, err := f.Get(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}

	into, err := blob.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	for id, b := range got {
		put, _, err := into.Put(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}

		if put != id {
			t.Errorf("what arrived for %v was filed as %v; the two stores"+
				" disagree about what a blob is called", id, put)
		}
	}

	for _, id := range ids {
		want, err := from.Get(id)
		if err != nil {
			t.Fatal(err)
		}

		have, err := into.Get(id)
		if err != nil {
			t.Fatalf("%v did not arrive: %v", id, err)
		}

		if !bytes.Equal(want, have) {
			t.Errorf("%v differs between the two stores", id)
		}
	}
}

// A peer whose own disk has rotted answers with nothing, not with rubbish.
//
// `blob.Store.Get` verifies what it reads against the name it is filed under, so
// the **sender** catches its own decay. That is one of two checks and neither is
// redundant: this one catches an honest peer with a bad disk, and the receiver's
// (E238) catches a dishonest one. A fetch from a rotted peer therefore reports
// the blob missing rather than serving corruption that the far end has to
// notice.
func TestAPeerWithARottedDiskServesNothing(t *testing.T) {
	t.Parallel()

	from, dir, ids := storeWith(t, "one")

	// Decay it in place, under the name it is filed as.
	var found string

	err := filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && fi.Size() == 3 {
			found = p
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if found == "" {
		t.Fatal("the blob is not where this test expects it")
	}

	err = os.WriteFile(found, []byte("rot"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	src := &fleet.StoreSource{Label: "rotted", Blobs: from}

	// **The source, directly.** Going through a Fetch proves only that the
	// blob did not arrive - which is also true if the sender served rubbish and
	// the receiver rejected it, so the assertion would pass with the sender's
	// check deleted. It did (E240). What this test is named for is that the
	// *sender* offers nothing.
	offered, err := src.Fetch(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := offered[ids[0]]; ok {
		t.Error("a peer whose own disk has rotted offered the blob anyway;" +
			" its store told it the bytes do not match the name they are filed" +
			" under, and it served them regardless")
	}

	// And the fetch as a whole reports it missing, which is the caller-facing
	// half of the same fact.
	f := &fleet.Fetch{Peers: []fleet.Source{src}}

	_, err = f.Get(context.Background(), ids)
	if !errors.Is(err, fleet.ErrNotFetched) {
		t.Errorf("a rotted peer's fetch gave %v; it holds nothing usable and"+
			" should say so rather than serve what it has", err)
	}
}
