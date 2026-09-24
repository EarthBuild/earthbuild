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

	_, err = src.Fetch(t.Context(), ids)
	if err != nil {
		t.Fatalf("%v", err)
	}

	t.Logf("five whole layers in one request cost %v in total",
		time.Since(batched).Round(time.Microsecond))

	// The same answer with no transport at all, which separates what the server
	// computes from what the wire costs.
	alone := time.Now()

	for range rounds {
		_, _, err = held.Fragment(id, []string{"usr/lib/lib0.so"})
		if err != nil {
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

// **Structural rather than timed.** This asserted a ratio between two
// wall-clock measurements and failed under `-race -shuffle` on a busy CI runner
// while passing alone on a quiet laptop. A threshold on a clock measures the
// machine (E473, E481), and a *ratio* of two clocks is worse than one: the two
// halves drift independently, so the bound moves even when nothing regresses.
//
// The property was never about speed. Serving one file must not read the other
// three hundred and ninety-nine, and allocated bytes are that work directly -
// `fillContents` allocates each file it reads, so a fragment that read the whole
// layer allocates the whole layer. Load does not change that number.
//
// **Serial on purpose.** `runtime.MemStats` counts the process, so a parallel
// neighbour allocating mid-measurement would be charged to this store. Go pauses
// parallel tests for the serial pass, which is the only way to be alone in it.
func TestServingPartOfALayerDoesNotCostTheWholeLayer(t *testing.T) { //nolint:paralleltest // see the note above
	// Not parallel: the counter is process-wide; see above (paralleltest).
	const (
		files = 400
		each  = 16 << 10
	)

	held := layerStore(t)
	id := seedSizedLayer(t, held, files, each)

	one := []string{"usr/lib/lib0.so"}

	// Outside the measurement: the first fragment fills the manifest memo,
	// which is a once-per-layer cost and not what this is about.
	_, _, err := held.Fragment(id, one)
	if err != nil {
		t.Fatalf("%v", err)
	}

	before, ok := bytesRead(t)
	if !ok {
		t.Skip("nothing here counts this process's reads; the gate runs on Linux")
	}

	const rounds = 4

	for range rounds {
		_, _, fragErr := held.Fragment(id, one)
		if fragErr != nil {
			t.Fatalf("%v", fragErr)
		}
	}

	after, _ := bytesRead(t)
	per := (after - before) / rounds

	t.Logf("serving one %d-byte file of a %d-file layer reads %d bytes",
		each, files, per)

	// **Twenty times the file, against four hundred times it.** Serving one file
	// reads that file; serving it by reading the layer reads four hundred of
	// them. The walk itself reads a little - directory entries are bytes too -
	// so the bound is not one file exactly, but it sits an order of magnitude
	// below the wrong answer and an order above the right one. A bound with that
	// much room does not move when the machine is busy, which a clock does
	// (E473, E481): this test asserted a ratio between two wall-clock
	// measurements and failed under `-race -shuffle` on a loaded runner while
	// passing alone on a quiet laptop.
	if per > 20*each {
		t.Errorf("serving one %d-byte file read %d bytes, which is more of the"+
			" layer than the file - so a fragment is reading files nobody asked"+
			" for (E337, E338)", each, per)
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

		_, err := run(t.Context(), a)
		if err != nil {
			t.Fatalf("%v", err)
		}
	}

	if dials != 1 {
		t.Errorf("a worker dialled the same peer %d times for three steps"+
			"\n  a connection costs 25ms on loopback before it has moved"+
			" anything (E337)", dials)
	}
}
