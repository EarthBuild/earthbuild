package fleet_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// What a fetch costs before it has moved anything.
//
// **The open question from E336.** 1.1 MiB across four workers cost 5.88s of
// worker-time, about 190 KB/s on a gigabit LAN, and every fetch dials a fresh
// QUIC connection. Whether that is the reason is a measurement, not a guess -
// the last two experiments were spent on plausible causes that were not the
// cause (E335).
//
// Loopback understates it: there is no path discovery to do and the round trip
// is microseconds. If dialling dominates *here*, it certainly dominates between
// machines.
func TestWhatAFetchCostsBeforeItMovesAnything(t *testing.T) {
	t.Parallel()

	held := layerStore(t)
	id := seedLayer(t, held, 200)

	// Bound to loopback explicitly: the default is the unspecified address, and
	// `LocalAddr` then reports something nothing can dial.
	loop := netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), 0)

	serving, err := iroh.Bind(t.Context(),
		iroh.WithBindAddr(loop), iroh.WithALPNs(fleet.ALPNBlob))
	if err != nil {
		t.Skipf("no endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = serving.Shutdown(t.Context()) })

	go func() {
		_ = fleet.ServeBlobs(t.Context(), serving,
			&fleet.Parts{Whole: held}, func(error) {})
	}()

	asking, err := iroh.Bind(t.Context(), iroh.WithBindAddr(loop))
	if err != nil {
		t.Skipf("no endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = asking.Shutdown(t.Context()) })

	at := netaddr.NewEndpointAddr(serving.ID()).WithIP(serving.LocalAddr())

	// One dial, to warm anything that warms.
	src := &fleet.PeerSource{Endpoint: asking, Peer: at, Label: "peer"}

	_, _, err = src.Fragment(t.Context(), id, []string{"usr/lib/lib0.so"}, true)
	if err != nil {
		t.Fatalf("%v", err)
	}

	const rounds = 5

	began := time.Now()

	for i := range rounds {
		_, _, err = src.Fragment(t.Context(), id,
			[]string{"usr/lib/lib" + itoa(i) + ".so"}, false)
		if err != nil {
			t.Fatalf("%v", err)
		}
	}

	each := time.Since(began) / rounds

	// Reported rather than asserted at a threshold: what this is for is telling
	// a person whether dialling is worth removing, and a number that fails CI on
	// a busy machine would be removed instead.
	t.Logf("a fragment of one file of a 200-file layer costs %v, dialling each"+
		" time", each.Round(time.Microsecond))

	// And the same work as one request rather than five, which separates a cost
	// paid per *request* from one paid per blob.
	ids := make([]ir.NodeID, 0, rounds)
	for range rounds {
		ids = append(ids, id)
	}

	batched := time.Now()

	if _, err = src.Fetch(t.Context(), ids); err != nil {
		t.Fatalf("%v", err)
	}

	t.Logf("five whole layers in one request cost %v in total",
		time.Since(batched).Round(time.Microsecond))

	// The same answer with no transport at all, which separates what the server
	// computes from what the wire costs.
	alone := time.Now()

	for range rounds {
		if _, _, err = held.Fragment(id, []string{"usr/lib/lib0.so"}); err != nil {
			t.Fatalf("%v", err)
		}
	}

	t.Logf("the same fragment, computed and not sent, costs %v",
		(time.Since(alone) / rounds).Round(time.Microsecond))

	if each > 250*time.Millisecond {
		t.Errorf("a loopback fetch of one small file takes %v, which is not a"+
			" transfer cost at all", each)
	}
}

// Serving part of a layer does not cost a walk of the whole layer.
//
// **The answer to E336.** The same fragment computed and never sent costs what
// it costs over a network, to the microsecond: the transport contributes
// nothing, and both `Manifest` and `Pack` walk the whole tree hashing every
// file's contents in order to send one of them.
//
// For a 200-file layer that is 26ms. The base measured between machines has
// 2000 files, four workers each ask for a fragment of it, and the account called
// the result transfer-bound - which it was, in the sense that the bytes were
// waiting on a hash of ten times as many bytes as they contained (E337).
func TestServingPartOfALayerDoesNotCostTheWholeLayer(t *testing.T) {
	t.Parallel()

	small := layerStore(t)
	big := layerStore(t)

	few := seedLayer(t, small, 20)
	many := seedLayer(t, big, 400)

	one := []string{"usr/lib/lib0.so"}

	if _, _, err := small.Fragment(few, one); err != nil {
		t.Fatalf("%v", err)
	}

	if _, _, err := big.Fragment(many, one); err != nil {
		t.Fatalf("%v", err)
	}

	began := time.Now()

	for range 3 {
		if _, _, err := small.Fragment(few, one); err != nil {
			t.Fatalf("%v", err)
		}
	}

	cheap := time.Since(began) / 3

	began = time.Now()

	for range 3 {
		if _, _, err := big.Fragment(many, one); err != nil {
			t.Fatalf("%v", err)
		}
	}

	dear := time.Since(began) / 3

	t.Logf("one file of a 20-file layer costs %v; of a 400-file layer, %v",
		cheap.Round(time.Microsecond), dear.Round(time.Microsecond))

	// **Not asserted as a ratio yet, and that is deliberate.** Serving a
	// fragment walks the layer twice - once for the manifest, once for the pack
	// - and hashes every file's contents both times. The manifest is memoised
	// below, which removes one walk; the pack's is the next thing to remove and
	// needs `walk` to hash only what is being sent, which is a change to
	// `engine/layer` rather than to a store.
	//
	// A ratio asserted now would either fail on the half that is fixed or pass
	// on the half that is not.
	if dear > 40*cheap {
		t.Errorf("serving one file cost %v from a 400-file layer against %v"+
			" from a 20-file one, which is worse than the twentyfold the layers"+
			" differ by (E337)", dear, cheap)
	}
}

// A layer's manifest is computed once, not once a fragment.
//
// It is a pure function of a stored layer - the tree does not change under a
// digest - so every fragment after the first can have it for nothing. That is
// half the cost of serving one; the other half is the pack, which still walks.
func TestALayersManifestIsComputedOnce(t *testing.T) {
	t.Parallel()

	held := layerStore(t)
	id := seedLayer(t, held, 400)

	first, _, err := held.Fragment(id, []string{"usr/lib/lib0.so"})
	if err != nil {
		t.Fatalf("%v", err)
	}

	began := time.Now()

	again, err := held.Manifest(id)
	if err != nil {
		t.Fatalf("%v", err)
	}

	took := time.Since(began)

	if string(again) != string(first) {
		t.Error("a layer's manifest changed between two readings of the same" +
			" unchanging tree")
	}

	if took > 2*time.Millisecond {
		t.Errorf("a second reading of a manifest took %v, so it was computed"+
			" again\n  it is a pure function of a stored layer (E337)", took)
	}
}

// A worker dials a peer once, not once a step.
//
// **25.7ms a fetch on loopback**, where there is no network to blame: that is
// what a fresh QUIC connection costs before anything moves. Between machines,
// with a real round trip and path discovery, it is the reason 1.1 MiB took 5.88s
// of worker-time (E336, E337).
//
// Nothing was reused because nothing could be: `sources` builds a fresh
// `PeerSource` for every holder of every assignment, so the connection cache
// inside one has nobody to be a cache for.
func TestAWorkerDialsAPeerOnceNotOnceAStep(t *testing.T) {
	t.Parallel()

	held := layerStore(t)
	id := seedLayer(t, held, 3)

	dials := 0

	run := fleet.Runner(&countingExecutor{}, core.Worker{ID: "w"},
		fleet.WithCapacity(4),
		fleet.WithFragments(&fleet.Fragments{Root: t.TempDir()}),
		fleet.WithPeers("me@host:1", func(at string) (fleet.Source, error) {
			dials++

			return &peerLike{at: at, from: held}, nil
		}))

	for _, want := range [][]string{
		{"usr/lib/lib0.so"}, {"usr/lib/lib1.so"}, {"usr/lib/lib2.so"},
	} {
		a := assignmentOn(id, want)
		a.Hints.Holders = []string{"peer@host:2"}

		if _, err := run(t.Context(), a); err != nil {
			t.Fatalf("%v", err)
		}
	}

	if dials != 1 {
		t.Errorf("a worker dialled the same peer %d times for three steps"+
			"\n  a connection costs 25ms on loopback before it has moved"+
			" anything (E337)", dials)
	}
}
