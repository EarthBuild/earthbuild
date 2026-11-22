package fleet_test

import (
	"bytes"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A fragment is never visible as the layer it came from.
//
// **The property everything else depends on.** A layer is named by the digest of
// its whole tree, so a store that let a fragment answer to the layer's name would
// serve part of a base to every later build as though it were the base - and the
// build would succeed (E282).
//
// Kept apart by construction rather than by discipline: fragments live somewhere
// `LayerStore.Has` does not look.
func TestAFragmentIsNeverVisibleAsItsLayer(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := aLayer(t, root)

	m, packed := fragmentAndManifest(t, root, id, []string{"etc/hosts"})

	mine := t.TempDir()
	frags := &fleet.Fragments{Root: mine}
	layers := &fleet.Layers{Root: mine}

	err := frags.PutVerified(id, []string{"etc/hosts"}, m, bytes.NewReader(packed))
	if err != nil {
		t.Fatalf("keeping a fragment: %v", err)
	}

	if !frags.Has(id, []string{"etc/hosts"}) {
		t.Error("the fragment was not kept")
	}

	if layers.Has(id) {
		t.Fatal("a fragment answers to its layer's name" +
			"\n  every later build would take part of a base for the base")
	}
}

// The same paths in a different order are the same fragment.
//
// The name has to be a function of what the fragment *contains*, or two
// predictions listing one set of paths differently would fetch it twice and keep
// both - which is the cache growing rather than being used, the failure E262's
// determinism rule exists to prevent one level down.
func TestTheSamePathsInAnyOrderAreOneFragment(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := aLayer(t, root)

	mine := &fleet.Fragments{Root: t.TempDir()}

	m, packed := fragmentAndManifest(t, root, id, []string{"etc/hosts"})

	err := mine.PutVerified(id, []string{"etc", "etc/hosts"}, m, bytes.NewReader(packed))
	if err != nil {
		t.Fatal(err)
	}

	if !mine.Has(id, []string{"etc/hosts", "etc"}) {
		t.Error("one set of paths, listed in two orders, is two fragments")
	}

	// And a genuinely different set is genuinely different.
	if mine.Has(id, []string{"etc/hosts", "usr"}) {
		t.Error("a fragment answered for paths it does not contain")
	}
}

// A fragment of one layer is not a fragment of another.
//
// Both halves of the name matter. Two bases commonly share a path - `/etc/hosts`
// is in every image - and a fragment named only by its paths would serve one
// image's file as another's.
func TestAFragmentOfOneLayerIsNotAFragmentOfAnother(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := aLayer(t, root)

	other := ir.NodeID{99}

	mine := &fleet.Fragments{Root: t.TempDir()}

	m, packed := fragmentAndManifest(t, root, id, []string{"etc/hosts"})

	err := mine.PutVerified(id, []string{"etc/hosts"}, m, bytes.NewReader(packed))
	if err != nil {
		t.Fatal(err)
	}

	if mine.Has(other, []string{"etc/hosts"}) {
		t.Error("one image's /etc/hosts answered for another's")
	}
}

// A fragment that does not arrive whole leaves nothing.
//
// The same discipline as a layer (E263): a half-unpacked fragment sitting under
// its name would be used, and it would be missing exactly the files that were
// being fetched when the transfer stopped.
func TestAPartialFragmentIsNotLeftBehind(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := aLayer(t, root)

	m, packed := fragmentAndManifest(t, root, id, []string{"etc/hosts"})

	mine := &fleet.Fragments{Root: t.TempDir()}

	err := mine.PutVerified(id, []string{"etc/hosts"}, m,
		bytes.NewReader(packed[:len(packed)*2/3]))
	if err == nil {
		t.Fatal("a truncated fragment was accepted")
	}

	if mine.Has(id, []string{"etc/hosts"}) {
		t.Error("a partial fragment is sitting under its name")
	}
}

// The test that stood here recorded that a fragment reaching this store was not
// checked against its layer; see TestAFragmentIsCheckedAgainstItsManifest and
// TestAFragmentOfAnotherTreeIsRefused, which is where that check lives now.
//
// It was E282's gap, closed by `layer.Manifest` and `layer.VerifyFragment`
// (E284) and wired into this store as `Fragments.PutVerified` - which takes the
// manifest and keeps nothing that does not answer to it. The comment outlived
// both the test and the gap, describing an open hole in the present tense two
// increments after it was filled (E481).
