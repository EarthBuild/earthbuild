package fleet_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// fromStore answers fragment requests out of a real layer store.
type fromStore struct {
	layers *fleet.Layers
	asked  int
}

func (f *fromStore) Fragment(
	_ context.Context, id ir.NodeID, want []string, proof bool,
) ([]byte, []byte, error) {
	f.asked++

	m, p, err := f.layers.Fragment(id, want)
	if err != nil || proof {
		return m, p, err
	}

	// Honours the flag, as the wire does. A fake that sent the manifest anyway
	// would make a mechanism that saves the dominant cost look like it saves
	// nothing - and the fake would be the thing that was wrong (E261, E299).
	return nil, p, nil
}

// nothing answers nothing, as an unreachable peer does.
type nothing struct{ asked int }

func (n *nothing) Fragment(
	context.Context, ir.NodeID, []string, bool,
) ([]byte, []byte, error) {
	n.asked++

	return nil, nil, errors.New("no fragment here")
}

// wrong answers with a coherent fragment of a different layer.
type wrong struct {
	layers *fleet.Layers
	other  ir.NodeID
	asked  int
}

func (w *wrong) Fragment(
	_ context.Context, _ ir.NodeID, want []string, _ bool,
) ([]byte, []byte, error) {
	w.asked++

	return w.layers.Fragment(w.other, want)
}

// A worker fetches the part of each input its step was predicted to read.
//
// The fetch side of lazy transfer, and the shape is `Provision`'s: the same
// ordered sources, the same skip-what-is-here, the same store-as-you-go - with
// the layer replaced by the part of it somebody asked for (E288).
func TestAWorkerFetchesThePartItWasToldAbout(t *testing.T) {
	t.Parallel()

	theirs := t.TempDir()
	id := aBiggerLayer(t, theirs)

	src := &fromStore{layers: &fleet.Layers{Root: theirs}}

	mine := &fleet.Fragments{Root: t.TempDir()}

	a := fleet.Assignment{
		Version: fleet.Version,
		Base:    []ir.NodeID{id},
		Hints:   fleet.Hints{ReadsPredicted: []string{"etc/hosts"}},
	}

	moved, err := fleet.ProvisionFragments(t.Context(), mine, a, src)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if !mine.Has(id, []string{"etc/hosts"}) {
		t.Fatal("the fragment did not arrive")
	}

	if moved.Bytes == 0 {
		t.Error("a fragment arrived and was accounted as free")
	}

	// Far less than the layer, which is the entire point.
	whole, err := (&fleet.Layers{Root: theirs}).Get(id)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("moved %d bytes of a %d byte layer", moved.Bytes, len(whole))

	if moved.Bytes >= int64(len(whole)) {
		t.Errorf("moved %d bytes for a layer of %d", moved.Bytes, len(whole))
	}
}

// A source that answers with somebody else's layer is skipped.
//
// I6 on the fragment path. The forgery here is the plausible one: a *coherent*
// fragment of a different layer, with its own honest manifest - which every
// check but "does this manifest hash to the layer I asked about" would pass
// (E285).
func TestASourceAnsweringWithAnotherLayerIsSkipped(t *testing.T) {
	t.Parallel()

	theirs := t.TempDir()
	id := aBiggerLayer(t, theirs)
	other := aLayerWithContent(t, theirs, "an entirely different image")

	layers := &fleet.Layers{Root: theirs}

	liar := &wrong{layers: layers, other: other}
	honest := &fromStore{layers: layers}

	mine := &fleet.Fragments{Root: t.TempDir()}

	a := fleet.Assignment{
		Version: fleet.Version,
		Base:    []ir.NodeID{id},
		Hints:   fleet.Hints{ReadsPredicted: []string{"etc/hosts"}},
	}

	_, err := fleet.ProvisionFragments(t.Context(), mine, a, liar, honest)
	if err != nil {
		t.Fatalf("a lying source failed the fetch instead of costing a retry: %v", err)
	}

	if liar.asked == 0 {
		t.Error("the liar was never asked, so nothing was skipped")
	}

	if honest.asked == 0 {
		t.Error("the honest source was never reached")
	}

	if !mine.Has(id, []string{"etc/hosts"}) {
		t.Error("the fragment did not arrive from the source that had it")
	}
}

// What is already here is not fetched again.
func TestAFragmentAlreadyHereIsNotFetchedAgain(t *testing.T) {
	t.Parallel()

	theirs := t.TempDir()
	id := aBiggerLayer(t, theirs)

	layers := &fleet.Layers{Root: theirs}
	mine := &fleet.Fragments{Root: t.TempDir()}

	m, packed, err := layers.Fragment(id, []string{"etc/hosts"})
	if err != nil {
		t.Fatal(err)
	}

	err = mine.PutVerified(id, []string{"etc/hosts"}, m, bytes.NewReader(packed))
	if err != nil {
		t.Fatal(err)
	}

	src := &nothing{}

	a := fleet.Assignment{
		Version: fleet.Version,
		Base:    []ir.NodeID{id},
		Hints:   fleet.Hints{ReadsPredicted: []string{"etc/hosts"}},
	}

	moved, err := fleet.ProvisionFragments(t.Context(), mine, a, src)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if src.asked != 0 {
		t.Errorf("asked %d time(s) for a fragment already here", src.asked)
	}

	if moved.Bytes != 0 {
		t.Errorf("accounted %d bytes for a fetch that did not happen", moved.Bytes)
	}
}

// With nothing predicted there is no fragment to ask for.
//
// Not an empty request: a worker that has not been told what its step reads has
// to fetch the layer, and this says so by doing nothing rather than by fetching
// a fragment of nothing.
func TestWithNothingPredictedNoFragmentIsAskedFor(t *testing.T) {
	t.Parallel()

	src := &nothing{}

	a := fleet.Assignment{Version: fleet.Version, Base: []ir.NodeID{{1}}}

	moved, err := fleet.ProvisionFragments(t.Context(),
		&fleet.Fragments{Root: t.TempDir()}, a, src)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if src.asked != 0 {
		t.Errorf("asked %d time(s) with nothing predicted", src.asked)
	}

	if moved.Bytes != 0 {
		t.Errorf("accounted %d bytes", moved.Bytes)
	}
}
