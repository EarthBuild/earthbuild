package fleet_test

import (
	"bytes"
	"context"
	"errors"
	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"
	"net/netip"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/blob"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// blobPeers is a store on one endpoint and a source pointed at it.
func blobPeers(t *testing.T, bodies ...string) (*fleet.Fetch, *blob.Store, []ir.NodeID) {
	t.Helper()

	client, server := endpointsFor(t, fleet.ALPNBlob)

	store, _, ids := storeWith(t, bodies...)

	go func() {
		_ = fleet.ServeBlobs(t.Context(), server, store,
			func(err error) { t.Logf("server: %v", err) })
	}()

	src := &fleet.PeerSource{
		Label: "peer", Endpoint: client, Peer: loopback(t, server),
	}

	return &fleet.Fetch{Peers: []fleet.Source{src}}, store, ids
}

// A blob crosses the network and verifies on arrival.
//
// C.4's transfer, between two endpoints. The digest is checked on receipt by the
// same `VerifiedCopy` a local fetch uses (E239), so nothing about crossing a
// network changes what is believed - which is the point of content addressing at
// the boundary.
func TestABlobCrossesTheWire(t *testing.T) {
	t.Parallel()

	f, store, ids := blobPeers(t, "one", "two", "three")

	got := retryFetch(t, f, ids)

	for _, id := range ids {
		want, err := store.Get(id)
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(got[id], want) {
			t.Errorf("%v arrived as %q, want %q", id, got[id], want)
		}
	}
}

// A blob the peer does not have is an absence, not a hang.
//
// The answer is a flag per requested blob, in the order they were asked for, so
// a missing one is a byte rather than a gap the receiver has to detect by
// running out of stream. A protocol that simply omitted it would leave the
// reader taking the *next* blob's bytes for this one's.
func TestABlobThePeerLacksIsAnAbsence(t *testing.T) {
	t.Parallel()

	f, _, ids := blobPeers(t, "one")

	absent := fleet.BlobID([]byte("nobody has this"))

	// Asked for in the middle, so a mishandled absence corrupts what follows.
	got, err := f.Get(t.Context(), []ir.NodeID{ids[0], absent})
	if !errors.Is(err, fleet.ErrNotFetched) {
		t.Fatalf("a missing blob gave %v, want ErrNotFetched", err)
	}

	if !bytes.Equal(got[ids[0]], []byte("one")) {
		t.Errorf("the blob that was present arrived as %q; an absence took the"+
			" next blob's bytes with it", got[ids[0]])
	}
}

// retryFetch waits for the server goroutine to be accepting.
//
// The same synchronisation the control protocol needs and for the same reason: a
// connection to an endpoint that has not reached Accept is refused rather than
// queued (E247).
func retryFetch(t *testing.T, f *fleet.Fetch, ids []ir.NodeID) map[ir.NodeID][]byte {
	t.Helper()

	var (
		got map[ir.NodeID][]byte
		err error
	)

	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		got, err = f.Get(t.Context(), ids)
		if err == nil {
			return got
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("the blobs never arrived: %v", err)

	return nil
}

// A peer that stops answering is not a peer that has nothing.
//
// `PeerSource.Fetch` treated a read failure as a short answer - "what arrived is
// still useful, and the caller will ask somebody else" - which is right about
// *what to do* and wrong about *what to say*. A connection that times out
// mid-answer and a peer that genuinely lacks the blob became the same thing, and
// the caller reported "no source had it" for a network that had gone away
// (E311).
//
// The bytes that arrived are still returned, because they are still useful. The
// error comes with them.
func TestAPeerThatStopsAnsweringIsNotAPeerThatHasNothing(t *testing.T) {
	t.Parallel()

	local := netip.AddrPortFrom(netip.IPv6Loopback(), 0)

	holder, err := iroh.Bind(t.Context(), iroh.WithBindAddr(local),
		iroh.WithALPNs(fleet.ALPNBlob))
	if err != nil {
		t.Skipf("no endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = holder.Shutdown(context.WithoutCancel(t.Context())) })

	// Accepts, and then says nothing at all.
	go func() {
		conn, aerr := holder.Accept(t.Context())
		if aerr != nil {
			return
		}

		_, _ = conn.AcceptStream(t.Context())

		<-t.Context().Done()
	}()

	asker, err := iroh.Bind(t.Context(), iroh.WithBindAddr(local))
	if err != nil {
		t.Skipf("no second endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = asker.Shutdown(context.WithoutCancel(t.Context())) })

	src := &fleet.PeerSource{
		Endpoint: asker,
		Peer:     netaddr.NewEndpointAddr(holder.ID()).WithIP(holder.LocalAddr()),
		Label:    "silent",
	}

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	_, err = src.Fetch(ctx, []ir.NodeID{{1}})
	if err == nil {
		t.Error("a peer that never answered was reported as a peer without the" +
			" blob\n  the caller then says \"no source had it\" about a network" +
			" that went away")
	}
}
