package fleet_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// fakeSource has some blobs and counts how often it was asked.
type fakeSource struct {
	name  string
	has   map[ir.NodeID][]byte
	calls int
	asked int // how many ids it was asked for, across all calls
	err   error
	// corrupt returns bytes that are not what was asked for.
	corrupt bool
	// order is the shared log, so the sequence of sources can be asserted.
	order *[]string
}

func (s *fakeSource) Name() string { return s.name }

func (s *fakeSource) Fetch(
	_ context.Context, ids []ir.NodeID,
) (map[ir.NodeID]io.Reader, error) {
	s.calls++
	s.asked += len(ids)

	if s.order != nil {
		*s.order = append(*s.order, s.name)
	}

	if s.err != nil {
		return nil, s.err
	}

	out := map[ir.NodeID]io.Reader{}

	for _, id := range ids {
		b, ok := s.has[id]
		if !ok {
			continue
		}

		// Plain bytes: a source hands back what was asked for, having already
		// checked it survived the journey (E264). The chunk-by-chunk check
		// belongs to the transport, which is the only place there is a journey.
		body := b

		if s.corrupt {
			// Somebody else's blob, not random bytes. The tempting fake is
			// rubbish, and it is weaker: rubbish is refused by anything that
			// looks at the bytes at all, while this is a perfectly good blob -
			// of something nobody asked for.
			body = []byte("not what you asked for")
		}

		out[id] = bytes.NewReader(body)
	}

	return out, nil
}

func blobs(bodies ...string) (map[ir.NodeID][]byte, []ir.NodeID) {
	m := map[ir.NodeID][]byte{}
	ids := make([]ir.NodeID, 0, len(bodies))

	for _, b := range bodies {
		id := fleet.BlobID([]byte(b))
		m[id] = []byte(b)
		ids = append(ids, id)
	}

	return m, ids
}

// Peers holding the blob, then other peers, then the registry.
//
// C.4's order, and **multi-source fallback is what makes registry availability
// non-load-bearing** (I6): a build whose registry is down proceeds if any peer
// has what it needs, which is the difference between a shared cache and a single
// point of failure.
func TestTheRegistryIsAskedLast(t *testing.T) {
	t.Parallel()

	have, ids := blobs("one")

	var order []string

	holder := &fakeSource{name: "holder", has: have, order: &order}
	peer := &fakeSource{name: "peer", has: have, order: &order}
	registry := &fakeSource{name: "registry", has: have, order: &order}

	f := &fleet.Fetch{
		Holders: []fleet.Source{holder}, Peers: []fleet.Source{peer},
		Registry: registry,
	}

	got, err := f.Get(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 {
		t.Fatalf("fetched %d blobs, want 1", len(got))
	}

	if len(order) != 1 || order[0] != "holder" {
		t.Errorf("the sources asked were %v; a peer announced as holding the"+
			" blob answers without anybody going further", order)
	}
}

// A registry that is down does not fail a build a peer can serve.
func TestARegistryThatIsDownIsNotLoadBearing(t *testing.T) {
	t.Parallel()

	have, ids := blobs("one", "two")

	// The failing source is a **holder**, so it is reached. The first version of
	// this test put it in Registry, which is asked last - and the peer before it
	// satisfied everything, so the source that was supposed to be down was never
	// asked at all. It passed with the tolerance deleted, which is the class of
	// test this work keeps finding: one whose subject is never reached (E237).
	down := &fakeSource{name: "down", err: errors.New("502")}
	peer := &fakeSource{name: "peer", has: have}

	f := &fleet.Fetch{
		Holders: []fleet.Source{down}, Peers: []fleet.Source{peer},
	}

	got, err := f.Get(context.Background(), ids)
	if err != nil {
		t.Fatalf("a source was down and the fetch failed: %v"+
			"\n  I6 is that no single source is load-bearing", err)
	}

	if down.calls != 1 {
		t.Fatalf("the failing source was asked %d times; if it is never asked,"+
			" this test proves only that a working peer works", down.calls)
	}

	if len(got) != 2 {
		t.Errorf("fetched %d blobs, want 2", len(got))
	}
}

// Blobs are requested in batches, not one stream each.
//
// C.4's first sentence: "One stream per blob does not survive a thousand-blob
// synchronisation." Asserted by counting *calls* against *ids*, because a source
// asked once for fifty is the property and a source asked fifty times for one
// would satisfy any test that only checked the blobs arrived.
func TestBlobsAreRequestedInBatches(t *testing.T) {
	t.Parallel()

	bodies := make([]string, 50)
	for i := range bodies {
		bodies[i] = string(rune('a'+i%26)) + string(rune('0'+i/26))
	}

	have, ids := blobs(bodies...)

	peer := &fakeSource{name: "peer", has: have}
	f := &fleet.Fetch{Peers: []fleet.Source{peer}}

	got, err := f.Get(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != len(ids) {
		t.Fatalf("fetched %d of %d", len(got), len(ids))
	}

	if peer.calls != 1 {
		t.Errorf("the source was asked %d times for %d blobs; one stream per"+
			" blob does not survive a thousand-blob synchronisation",
			peer.calls, len(ids))
	}
}

// A source serving wrong bytes is skipped, and the rest is not poisoned.
//
// The digest is checked here rather than trusted, which is what makes fetching
// from an untrusted peer safe at all (§2.1). A peer serving corruption is a peer
// problem: the blob is taken from somebody else rather than the fetch failing,
// because I6's point is that no single source is load-bearing - including a
// dishonest one.
func TestASourceServingWrongBytesIsSkipped(t *testing.T) {
	t.Parallel()

	have, ids := blobs("one", "two")

	liar := &fakeSource{name: "liar", has: have, corrupt: true}
	honest := &fakeSource{name: "honest", has: have}

	f := &fleet.Fetch{
		Holders: []fleet.Source{liar}, Peers: []fleet.Source{honest},
	}

	got, err := f.Get(context.Background(), ids)
	if err != nil {
		t.Fatalf("a lying peer failed the fetch: %v", err)
	}

	for _, id := range ids {
		if fleet.BlobID(got[id]) != id {
			t.Errorf("the blob served for %v does not hash to it; corruption"+
				" reached the caller", id)
		}
	}

	if honest.asked != 2 {
		t.Errorf("the honest source was asked for %d blobs, want 2; a liar's"+
			" answers must not be counted as delivered", honest.asked)
	}
}

// What could not be fetched is named.
func TestAMissingBlobIsReportedRatherThanReturnedEmpty(t *testing.T) {
	t.Parallel()

	have, ids := blobs("one", "two")
	partial := map[ir.NodeID][]byte{ids[0]: have[ids[0]]}

	f := &fleet.Fetch{
		Peers: []fleet.Source{&fakeSource{name: "peer", has: partial}},
	}

	got, err := f.Get(context.Background(), ids)
	if !errors.Is(err, fleet.ErrNotFetched) {
		t.Fatalf("a missing blob gave %v, want ErrNotFetched", err)
	}

	// And what *was* fetched comes back, because a caller with one of two blobs
	// is better placed than one with none and an error.
	if len(got) != 1 {
		t.Errorf("the fetch returned %d blobs; what arrived is still useful",
			len(got))
	}
}
