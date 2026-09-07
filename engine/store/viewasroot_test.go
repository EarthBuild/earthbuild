package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// The view digests a shared store the way the guest sees it.
//
// Κ₂ compares what a step *observed* against what a rebuilt step *would see*,
// and both of those happen inside the sandbox. On darwin the store is shared
// into the VM with everything owned by root, so the guest digests uid 0 where
// the host digests the invoking user - a constant offset that made every base
// look changed and the tier unable to serve a single RUN (E494).
//
// The guest cannot fix it from its side: the shift is done by the sharing
// mechanism rather than a user namespace, so `/proc/self/uid_map` is the
// identity and there is nothing to read. The host knows, because the host is
// what shares it.
func TestAViewDigestsAStoreSharedAsRootTheWayAGuestSeesIt(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	id := ir.NodeID{7}

	root := filepath.Join(store, "layers", id.String())
	err := os.MkdirAll(filepath.Join(root, "bin"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	file := filepath.Join(root, "bin", "cat")
	err = os.WriteFile(file, []byte("#!/bin/sh\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// What a guest sees: this file, with its owner read as root.
	asGuest := digestAsRoot(t, file)

	// The plain view is the store's own answer, which is what the observation
	// could never match.
	plain, err := LayerStore(store).View(context.Background(), []ir.NodeID{id})
	if err != nil {
		t.Fatal(err)
	}

	stored, ok := plain.Digest("/bin/cat")
	if !ok {
		t.Fatal("the plain view has no /bin/cat")
	}

	seen, err := LayerStore(store).
		SeenAsRoot(uint32(os.Getuid()), uint32(os.Getgid())).
		View(context.Background(), []ir.NodeID{id})
	if err != nil {
		t.Fatal(err)
	}

	got, ok := seen.Digest("/bin/cat")
	if !ok {
		t.Fatal("the shared-as-root view has no /bin/cat")
	}

	if got != asGuest {
		t.Errorf("the view digests /bin/cat as %s and a guest sees %s",
			got.String()[:12], asGuest.String()[:12])
	}

	// And the two views differ, or the test would pass with the mapping doing
	// nothing - which is what a machine whose files are already root-owned
	// would produce, and the reason this skips there rather than claiming a
	// pass.
	if os.Getuid() == 0 {
		t.Skip("running as root, so there is no shift to apply")
	}

	if got == stored {
		t.Error("the shared-as-root view and the plain one agree, so the" +
			" mapping reached nothing")
	}
}

// digestAsRoot is what the same file digests to with its owner read as root.
func digestAsRoot(t *testing.T, path string) ir.NodeID {
	t.Helper()

	// "inside outside count": the id a guest sees, the id the store holds.
	m, err := layer.ParseIDMap(strings.NewReader(
		itoa(os.Getuid()) + " 0 1\n"))
	if err != nil {
		t.Fatal(err)
	}

	g, err := layer.ParseIDMap(strings.NewReader(
		itoa(os.Getgid()) + " 0 1\n"))
	if err != nil {
		t.Fatal(err)
	}

	d, err := layer.PathDigestIn(path, m, g)
	if err != nil {
		t.Fatal(err)
	}

	return d
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}

	var b []byte

	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}

	return string(b)
}
