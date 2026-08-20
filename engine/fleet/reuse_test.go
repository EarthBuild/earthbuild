package fleet_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A manifest crosses once per layer, not once per fragment.
//
// **The measurement said so.** A manifest is about a hundred bytes an entry, and
// a fragment is the bytes actually read - so for the case lazy transfer exists
// for, a small read set from a large base, *the manifest is the dominant cost*:
// five thousand files and ten paths read moved 534 KB of manifest against 83 KB
// of content (E298).
//
// It only has to cross once. A worker that has the manifest for a layer has it
// for every fragment of that layer, and the client says so by asking for the
// fragment alone.
func TestAManifestCrossesOncePerLayer(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	id := aBaseOf(t, store, 500)

	src := &countingFragmenter{inner: &fromStore{layers: &fleet.Layers{Root: store}}}

	mine := &fleet.Fragments{Root: t.TempDir()}

	ask := func(paths ...string) {
		t.Helper()

		_, err := fleet.ProvisionFragments(context.Background(), mine,
			fleet.Assignment{
				Base:  []ir.NodeID{id},
				Hints: fleet.Hints{ReadsPredicted: paths},
			}, src)
		if err != nil {
			t.Fatal(err)
		}
	}

	ask("usr/lib/lib1.so")

	first := src.bytes

	ask("usr/lib/lib2.so")

	second := src.bytes - first

	t.Logf("first fragment %d bytes, second %d", first, second)

	if second >= first/2 {
		t.Errorf("the second fragment of one layer moved %d bytes against the"+
			" first's %d\n  the manifest is being sent again, and it is the"+
			" dominant cost of a small read set", second, first)
	}
}

// A manifest nobody has yet still crosses.
//
// The reuse is an optimisation and must not become a requirement: a worker that
// has never seen a layer needs the proof, and asking for a fragment without one
// would leave it unable to verify what arrives.
func TestAManifestNobodyHasStillCrosses(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	id := aBaseOf(t, store, 50)

	src := &countingFragmenter{inner: &fromStore{layers: &fleet.Layers{Root: store}}}

	mine := &fleet.Fragments{Root: t.TempDir()}

	_, err := fleet.ProvisionFragments(context.Background(), mine,
		fleet.Assignment{
			Base:  []ir.NodeID{id},
			Hints: fleet.Hints{ReadsPredicted: []string{"usr/lib/lib1.so"}},
		}, src)
	if err != nil {
		t.Fatal(err)
	}

	if !mine.HasManifest(id) {
		t.Fatal("a first fetch left this worker without the proof")
	}

	if src.bytes == 0 {
		t.Error("nothing crossed at all")
	}
}

// The manifest kept is the one that hashes to the layer.
//
// Keeping an unverified manifest would be keeping a forgery for every later
// fragment of that layer to be checked against - which is worse than not keeping
// one, because it is checked once and trusted for ever after.
func TestTheKeptManifestIsTheVerifiedOne(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	real := aBaseOf(t, store, 20)
	other := aBaseOf(t, store, 21)

	layers := &fleet.Layers{Root: store}

	liar := &wrong{layers: layers, other: other}

	mine := &fleet.Fragments{Root: t.TempDir()}

	_, err := fleet.ProvisionFragments(context.Background(), mine,
		fleet.Assignment{
			Base:  []ir.NodeID{real},
			Hints: fleet.Hints{ReadsPredicted: []string{"usr/lib/lib1.so"}},
		}, liar)
	if err == nil {
		t.Fatal("a fragment of another layer was accepted")
	}

	if mine.HasManifest(real) {
		t.Error("and its manifest was kept as this layer's proof" +
			"\n  every later fragment would be checked against a forgery")
	}
}
