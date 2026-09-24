package guest_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/decl"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// TestAGuestPacksALayerAWorkerCanUnpack.
//
// **A driver whose store is inside the VM cannot serve its own base.** On macOS
// the store is on the guest's block device by default, and for a correctness
// reason rather than a performance one - APFS is case-insensitive, so two files
// in a layer differing only in case collide on the way in. The fleet's blob
// server reads a host directory, which there holds nothing, so every worker
// refuses every step for want of a base the driver is holding (E-F1, F4).
//
// `PackLayer` already carries a layer out of such a store, as an OCI blob for
// `SAVE IMAGE`. The fleet speaks a different pack, so this is that same journey
// in the fleet's own format - and the property worth asserting is not the bytes
// but the round trip: what leaves a guest store has to arrive in a worker's
// store under the identity it left under.
func TestAGuestPacksALayerAWorkerCanUnpack(t *testing.T) {
	t.Parallel()

	guestStore := t.TempDir()
	id := aLayerIn(t, guestStore)

	var packed bytes.Buffer

	err := guest.PackFleetLayer(guestStore, id, &packed)
	if err != nil {
		t.Fatalf("packing out of the guest's store: %v", err)
	}

	workerStore := t.TempDir()

	got, _, err := (&fleet.Layers{Root: workerStore}).Put(bytes.NewReader(packed.Bytes()))
	if err != nil {
		t.Fatalf("a worker could not take what the guest packed: %v", err)
	}

	if got != id {
		t.Fatalf("it arrived as %v, sent as %v", got, id)
	}
}

// TestAGuestPacksADeclarationToo. A stack element need not be a layer, and the
// one that is not is the one that was missing when this was found.
func TestAGuestPacksADeclarationToo(t *testing.T) {
	t.Parallel()

	guestStore := t.TempDir()

	id, err := decl.Write(guestStore, decl.Declaration{
		Env: []string{"PATH=/usr/bin"}, WorkingDir: "/w",
	})
	if err != nil {
		t.Fatal(err)
	}

	var packed bytes.Buffer

	err = guest.PackFleetLayer(guestStore, id, &packed)
	if err != nil {
		t.Fatalf("packing a declaration out of the guest's store: %v", err)
	}

	workerStore := t.TempDir()

	got, _, err := (&fleet.Layers{Root: workerStore}).Put(bytes.NewReader(packed.Bytes()))
	if err != nil {
		t.Fatalf("a worker could not take the declaration: %v", err)
	}

	if got != id {
		t.Errorf("it arrived as %v, sent as %v", got, id)
	}
}

// aLayerIn files a small layer in a store and returns its identity.
func aLayerIn(t *testing.T, root string) ir.NodeID {
	t.Helper()

	made := t.TempDir()

	err := os.MkdirAll(filepath.Join(made, "etc"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(made, "etc", "hosts"),
		[]byte("127.0.0.1 localhost\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	c, err := layer.Take(made)
	if err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(root, "layers", c.ID.String())

	err = os.MkdirAll(filepath.Dir(dest), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Rename(made, dest)
	if err != nil {
		t.Fatal(err)
	}

	return c.ID
}

// TestAnElementGoesBackIntoTheGuestStore.
//
// A driver takes back what a worker produced (E274), and with the store inside
// the VM the host can no more write into it than read it. The round trip is the
// property: what a fleet packs, a guest stores, under the same identity.
func TestAnElementGoesBackIntoTheGuestStore(t *testing.T) {
	t.Parallel()

	theirs := t.TempDir()
	id := aLayerIn(t, theirs)

	packed, err := (&fleet.Layers{Root: theirs}).Get(id)
	if err != nil {
		t.Fatal(err)
	}

	guestStore := t.TempDir()

	got, n, err := guest.UnpackFleetLayer(guestStore, bytes.NewReader(packed))
	if err != nil {
		t.Fatalf("filing an element into the guest's store: %v", err)
	}

	if got != id {
		t.Fatalf("it arrived as %v, sent as %v", got, id)
	}

	if n <= 0 {
		t.Errorf("an element of %d bytes was filed", n)
	}

	// And it is servable from there, which is what makes the driver a peer.
	if !(&fleet.Layers{Root: guestStore}).Has(id) {
		t.Error("the guest's store took an element and does not hold it")
	}
}
