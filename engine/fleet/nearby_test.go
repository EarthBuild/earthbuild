package fleet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A blob this machine lacks comes from a peer.
//
// **The gap between a fleet that moves layers and one that moves a cache.**
// `Peers` refreshes per assignment and serves *fragments*; a cache's units and
// the helper that reads them are whole blobs, and nothing held a live list of
// who to ask for one.
func TestNearbyFetchesAWholeBlob(t *testing.T) {
	t.Parallel()

	body := []byte("a unit, or the module that knows what a unit is")
	id := ir.DigestOf(body)

	n := &Nearby{}
	n.Set([]Source{&saying{has: map[ir.NodeID][]byte{id: body}}})

	got, err := n.Node(context.Background(), id)
	if err != nil {
		t.Fatalf("fetch a blob a peer holds: %v", err)
	}

	if !bytes.Equal(got, body) {
		t.Errorf("served %q, want %q", got, body)
	}
}

// A peer that answers with the wrong bytes is not believed, and the next peer
// is asked.
//
// **A5 is scepticism here, not faith there** (§5.3). A source's own verification
// catches an honest peer with a bad disk; this catches a dishonest one, and a
// mismatch is a miss rather than an error - 𝔅's rule, so an attacker with total
// control of a peer can deny service and nothing else (I4).
func TestNearbyDoesNotBelieveAPeerThatLies(t *testing.T) {
	t.Parallel()

	body := []byte("what was asked for")
	id := ir.DigestOf(body)

	n := &Nearby{}
	n.Set([]Source{
		&saying{has: map[ir.NodeID][]byte{id: []byte("something else entirely")}},
		&saying{has: map[ir.NodeID][]byte{id: body}},
	})

	got, err := n.Node(context.Background(), id)
	if err != nil {
		t.Fatalf("a liar stopped the search: %v", err)
	}

	if !bytes.Equal(got, body) {
		t.Errorf("believed the liar: served %q", got)
	}
}

// Nobody to ask is an ordinary answer and says so.
func TestNearbyWithNobodyToAsk(t *testing.T) {
	t.Parallel()

	_, err := (&Nearby{}).Node(context.Background(), ir.DigestOf([]byte("x")))
	if !errors.Is(err, ErrNotFetched) {
		t.Errorf("an empty fleet reported %v, want ErrNotFetched", err)
	}
}

// A source that errors is skipped rather than fatal, for `sources`' reason: the
// address came from another machine's claim about itself.
func TestNearbySkipsASourceThatFails(t *testing.T) {
	t.Parallel()

	body := []byte("held by the second")
	id := ir.DigestOf(body)

	n := &Nearby{}
	n.Set([]Source{
		&saying{err: errors.New("unreachable")},
		&saying{has: map[ir.NodeID][]byte{id: body}},
	})

	if _, err := n.Node(context.Background(), id); err != nil {
		t.Errorf("an unreachable peer stopped the search: %v", err)
	}
}

// saying is a source holding exactly what it is given.
type saying struct {
	has map[ir.NodeID][]byte
	err error
}

func (s *saying) Name() string { return "saying" }

func (s *saying) Fetch(_ context.Context, ids []ir.NodeID) (map[ir.NodeID]io.Reader, error) {
	if s.err != nil {
		return nil, s.err
	}

	out := map[ir.NodeID]io.Reader{}

	for _, id := range ids {
		if b, ok := s.has[id]; ok {
			out[id] = bytes.NewReader(b)
		}
	}

	return out, nil
}
