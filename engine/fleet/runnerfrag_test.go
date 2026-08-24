package fleet_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A worker told what a step reads fetches only that, from its own sources.
//
// **Lazy transfer has never run over a real network.** The only thing that could
// do it was a shortcut in the probe's executor, dialling an endpoint chosen
// before the assignment arrived - the driver's *control* address, with a blob
// protocol it does not speak (E314) - so every lazy two-machine run so far
// silently fell back to whole layers.
//
// It belongs in `Runner`, which is where a worker's sources already are: the
// holders the driver named, corrected and dialled, with the driver last (C.4).
// Then a fragment from a peer is the same mechanism as a layer from a peer
// rather than a second one, and the case this exists for - a small read set from
// a large base - works between machines instead of only between goroutines.
func TestAWorkerFetchesOnlyWhatAStepReads(t *testing.T) {
	t.Parallel()

	held := layerStore(t)
	id := seedLayer(t, held, 3)

	into := &fleet.Fragments{Root: t.TempDir()}

	run := fleet.Runner(&countingExecutor{}, core.Worker{ID: "w"},
		fleet.WithFragments(into, localFragments{from: held}))

	reply, err := run(t.Context(), fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
		Base:    []ir.NodeID{id},
		Hints:   fleet.Hints{ReadsPredicted: []string{"usr/lib/lib0.so"}},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}

	if reply.Refused != "" {
		t.Fatalf("refused: %s", reply.Refused)
	}

	if !into.Has(id, []string{"usr/lib/lib0.so"}) {
		t.Fatal("the path the step was predicted to read did not arrive")
	}

	whole, err := held.Get(id)
	if err != nil {
		t.Fatalf("%v", err)
	}

	// The number is the point: a base is hundreds of megabytes and a step reads
	// a handful of it. One file of three must not cost three.
	if reply.FetchedBytes >= int64(len(whole)) {
		t.Errorf("fetching one file of three moved %d bytes and the whole layer"+
			" is %d\n  a worker that fetches everything is not lazy, it is"+
			" slower for the trouble (E323)", reply.FetchedBytes, len(whole))
	}
}

// layerStore is a Layers rooted in a directory that goes away with the test.
func layerStore(t *testing.T) *fleet.Layers {
	t.Helper()

	root := t.TempDir()

	err := os.MkdirAll(filepath.Join(root, "layers"), 0o750)
	if err != nil {
		t.Fatalf("%v", err)
	}

	return &fleet.Layers{Root: root}
}

// seedLayer files a layer of n distinct files and returns its id.
func seedLayer(t *testing.T, into *fleet.Layers, n int) ir.NodeID {
	t.Helper()

	tmp := t.TempDir()

	err := os.MkdirAll(filepath.Join(tmp, "usr", "lib"), 0o750)
	if err != nil {
		t.Fatalf("%v", err)
	}

	for i := range n {
		body := bytes.Repeat([]byte(fmt.Sprintf("%08d", i)), 512)

		err := os.WriteFile(
			filepath.Join(tmp, "usr", "lib", fmt.Sprintf("lib%d.so", i)),
			body, 0o600)
		if err != nil {
			t.Fatalf("%v", err)
		}
	}

	var packed bytes.Buffer

	err = layer.Pack(tmp, &packed)
	if err != nil {
		t.Fatalf("%v", err)
	}

	id, _, err := into.Put(&packed)
	if err != nil {
		t.Fatalf("%v", err)
	}

	return id
}

// localFragments serves fragments from a store in this process.
//
// `PeerSource` is both a Source and a Fragmenter, which is what a worker uses
// between machines; this is the same shape without a wire.
type localFragments struct{ from *fleet.Layers }

func (l localFragments) Fragment(
	_ context.Context, id ir.NodeID, want []string, proof bool,
) (manifest, packed []byte, err error) {
	manifest, packed, err = l.from.Fragment(id, want)
	if !proof {
		manifest = nil
	}

	return manifest, packed, err
}

// The fragment comes from the peers the driver named, not a second list.
//
// **The point of putting this in `Runner`.** A worker's sources are the holders
// the driver said were nearest, dialled and corrected, with the driver last
// (C.4). If fragments came from a separately-configured list instead, a fleet
// would be a mesh for whole layers and a star for parts of them - and the parts
// are the case that is supposed to be cheap.
//
// The fallback list is empty here on purpose: only a dialled holder can answer.
func TestAFragmentComesFromTheHoldersTheDriverNamed(t *testing.T) {
	t.Parallel()

	held := layerStore(t)
	id := seedLayer(t, held, 3)

	into := &fleet.Fragments{Root: t.TempDir()}

	asked := 0

	run := fleet.Runner(&countingExecutor{}, core.Worker{ID: "w"},
		fleet.WithFragments(into),
		fleet.WithPeers("me@host:1", func(at string) (fleet.Source, error) {
			asked++

			return &peerLike{at: at, from: held}, nil
		}))

	reply, err := run(t.Context(), fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
		Base:    []ir.NodeID{id},
		Hints: fleet.Hints{
			ReadsPredicted: []string{"usr/lib/lib1.so"},
			Holders:        []string{"peer@host:2"},
		},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}

	if reply.Refused != "" {
		t.Fatalf("refused: %s", reply.Refused)
	}

	if asked == 0 {
		t.Error("no holder was dialled for a fragment; a fleet that is a mesh" +
			" for layers and a star for parts of them has it backwards (E323)")
	}

	if !into.Has(id, []string{"usr/lib/lib1.so"}) {
		t.Error("the fragment did not arrive from the holder")
	}
}

// peerLike is what a dialled holder is: a source that can also send a part.
type peerLike struct {
	at   string
	from *fleet.Layers
}

func (p *peerLike) Name() string { return p.at }

func (p *peerLike) Fetch(
	context.Context, []ir.NodeID,
) (map[ir.NodeID]io.Reader, error) {
	return nil, errNoWholeLayers
}

func (p *peerLike) Fragment(
	_ context.Context, id ir.NodeID, want []string, proof bool,
) (manifest, packed []byte, err error) {
	manifest, packed, err = p.from.Fragment(id, want)
	if !proof {
		manifest = nil
	}

	return manifest, packed, err
}

var errNoWholeLayers = errors.New("this peer serves fragments only")
