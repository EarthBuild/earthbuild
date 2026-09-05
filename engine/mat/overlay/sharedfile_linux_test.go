package overlay

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A file nobody touched is the store's file, and saying so is the whole point:
// the alternative is shipping 45 MB back out of a VM to a host that already has
// it (E568).
// buildEarthly is the path these tests keep asking about: the artifact E568 is
// named for.
const buildEarthly = "build/earthly"

func TestAnUntouchedFileIsTheStoresOwn(t *testing.T) {
	t.Parallel()

	m, _ := materialiserFor(t)

	layer := ir.NodeID{1}
	err := m.WriteLayer(layer, map[string]string{buildEarthly: "bytes"})
	if err != nil {
		t.Fatal(err)
	}

	h := mountFor(t, m, layer)

	shared, ok := h.(core.SharedResolver)
	if !ok {
		t.Fatal("the handle does not resolve shared files")
	}

	rel, ok := shared.SharedFile(buildEarthly)
	if !ok {
		t.Fatal("an untouched file was not recognised as the store's own")
	}

	want := filepath.Join("layers", layer.String(), buildEarthly)
	if rel != want {
		t.Errorf("named %q, want %q", rel, want)
	}
}

// Every one of these is a case where answering yes would export the wrong
// bytes, so each is refused rather than reasoned about.
func TestWhatIsRefusedRatherThanResolved(t *testing.T) {
	t.Parallel()

	m, _ := materialiserFor(t)

	layer := ir.NodeID{2}
	err := m.WriteLayer(layer, map[string]string{
		buildEarthly: "bytes",
		"dir/inside": "bytes",
		"untouched":  "bytes",
	})
	if err != nil {
		t.Fatal(err)
	}

	h := mountFor(t, m, layer)

	// A copy-up: the step rewrote it, so the store's copy is stale.
	err = os.WriteFile(filepath.Join(h.Root(), "build/earthly"), []byte("rebuilt"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// A deletion: the merged view has no such file, and the store still does.
	err = os.Remove(filepath.Join(h.Root(), "untouched"))
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name string
		rel  string
	}{
		{"a file the step rewrote", "build/earthly"},
		{"a file the step deleted", "untouched"},
		{"a directory rather than a file", "dir"},
		{"a path that is not there at all", "absent"},
		{"the root itself", "."},
		{"an escape upwards", "../../etc/passwd"},
	} {
		if rel, ok := resolverOf(t, h).SharedFile(c.rel); ok {
			t.Errorf("%s: resolved to %q, want a refusal", c.name, rel)
		}
	}

	// The refusals must not be indiscriminate, or a passing test would only be
	// proving the feature is switched off.
	if _, ok := resolverOf(t, h).SharedFile("dir/inside"); !ok {
		t.Error("an untouched file beside the modified ones was refused too," +
			" so the refusals above prove nothing")
	}
}

// A scratch handle has no store behind it, so it has nothing to point at.
func TestAScratchHandleSharesNothing(t *testing.T) {
	t.Parallel()

	m, _ := materialiserFor(t)

	h, err := m.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = h.Release() }()

	err = os.WriteFile(filepath.Join(h.Root(), "f"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if rel, ok := resolverOf(t, h).SharedFile("f"); ok {
		t.Errorf("a scratch handle claimed %q is in the store", rel)
	}
}

func mountFor(t *testing.T, m *Materialiser, stack ...ir.NodeID) core.Handle {
	t.Helper()

	h, err := m.Materialise(context.Background(), stack)
	if err != nil {
		t.Skipf("this machine cannot mount overlayfs: %v", err)
	}

	t.Cleanup(func() { _ = h.Release() })

	return h
}

// resolverOf is the handle as a core.SharedResolver, or a failed test.
//
// A checked assertion rather than a bare one: a handle that stopped resolving
// shared files would otherwise panic here and read as a crash rather than as the
// capability having gone.
func resolverOf(t *testing.T, h core.Handle) core.SharedResolver {
	t.Helper()

	r, ok := h.(core.SharedResolver)
	if !ok {
		t.Fatal("the handle does not resolve shared files")
	}

	return r
}
