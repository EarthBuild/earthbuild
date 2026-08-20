package fleet_test

import (
	"bytes"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// What a step faults in comes from the same peers its base came from.
//
// **The production worker could not do this at all.** `earth-worker` builds its
// filler's sources once, before any assignment exists, from the driver's
// *control* identity - and `PeerSource` speaks the blob protocol, which that
// endpoint does not offer. It is the fault E314 found in the probe, sitting in
// the binary people would actually run: priming and fault-in have never worked
// between machines.
//
// The holders are per assignment and only `Runner` sees them. A sink is how they
// reach the executor without the executor knowing what a fleet is: the worker
// makes one, hands it to `Runner` and to its filler, and each assignment
// refreshes it.
func TestWhatAStepFaultsInComesFromItsOwnPeers(t *testing.T) {
	t.Parallel()

	held := layerStore(t)
	id := seedLayer(t, held, 3)

	var peers fleet.Peers

	run := fleet.Runner(&countingExecutor{}, core.Worker{ID: "w"},
		fleet.WithPeerSink(&peers),
		fleet.WithPeers("me@host:1", func(at string) (fleet.Source, error) {
			return &peerLike{at: at, from: held}, nil
		}))

	// Nothing has arrived yet, so there is nobody to ask - and saying so is the
	// point: a sink that answered before it had been filled would be a source
	// pointing at whatever was configured at start-up, which is the fault.
	if _, _, err := peers.Fragment(t.Context(), id, []string{"usr/lib/lib0.so"}, true); err == nil {
		t.Error("an empty sink answered for a layer")
	}

	_, err := run(t.Context(), fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
		Base:    []ir.NodeID{id},
		Hints:   fleet.Hints{Holders: []string{"peer@host:2"}},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}

	manifest, packed, err := peers.Fragment(t.Context(), id,
		[]string{"usr/lib/lib0.so"}, true)
	if err != nil {
		t.Fatalf("a step could not fault in from its own peers: %v", err)
	}

	into := &fleet.Fragments{Root: t.TempDir()}

	err = into.PutVerified(id, []string{"usr/lib/lib0.so"}, manifest,
		bytes.NewReader(packed))
	if err != nil {
		t.Errorf("what the sink served did not verify: %v", err)
	}
}
