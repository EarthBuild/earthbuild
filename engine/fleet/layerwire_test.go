package fleet_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A layer crosses a real connection and arrives as itself.
//
// The end of the road: E261 found that a layer could not move at all, E262 gave
// it a codec, E263 gave a store the ability to send and receive one, and every
// step of that was in one process. This is the one that makes a fleet of two
// machines able to build - and it is worth its seconds on a real QUIC connection
// because every previous stage passed against fakes that agreed with each other
// (E258, E261).
func TestALayerCrossesTheWireAndArrivesAsItself(t *testing.T) {
	t.Parallel()

	local := netip.AddrPortFrom(netip.IPv6Loopback(), 0)

	theirs := t.TempDir()
	id := aLayer(t, theirs)

	holder, err := iroh.Bind(t.Context(), iroh.WithBindAddr(local),
		iroh.WithALPNs(fleet.ALPNBlob))
	if err != nil {
		t.Skipf("no endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = holder.Shutdown(t.Context()) })

	go func() {
		_ = fleet.ServeBlobs(t.Context(), holder, &fleet.Layers{Root: theirs},
			func(err error) { t.Logf("holder: %v", err) })
	}()

	asker, err := iroh.Bind(t.Context(), iroh.WithBindAddr(local))
	if err != nil {
		t.Skipf("no second endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = asker.Shutdown(t.Context()) })

	mine := &fleet.Layers{Root: t.TempDir()}

	src := &fleet.PeerSource{
		Endpoint: asker,
		Peer:     netaddr.NewEndpointAddr(holder.ID()).WithIP(holder.LocalAddr()),
		Label:    "the other machine",
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	moved, err := fleet.Provision(ctx, mine,
		fleet.Assignment{Version: fleet.Version, Base: []ir.NodeID{id}}, src)
	if err != nil {
		t.Fatalf("provisioning across the wire: %v", err)
	}

	if !mine.Has(id) {
		t.Fatal("the layer did not arrive")
	}

	// Genuinely that layer, captured from what landed on disk - not a directory
	// with the right name, which is what a store trusting the wire would leave.
	got, err := layer.Take(mine.Root + "/layers/" + id.String())
	if err != nil {
		t.Fatal(err)
	}

	if got.ID != id {
		t.Errorf("what crossed is %v, filed as %v", got.ID, id)
	}

	if moved.Bytes == 0 {
		t.Error("a layer crossed the network and was accounted as free")
	}
}
