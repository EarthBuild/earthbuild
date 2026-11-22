package helper_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/blob"
	"github.com/EarthBuild/earthbuild/engine/helper"
)

// A cache mount goes into the blob store and comes back out on another machine.
//
// **The whole remit in one test.** A worker's cache directory is empty; the
// driver's holds a module. The units cross as content-addressed blobs - named by
// ℋ, dedupable, verifiable, and movable by transport that already existed - and
// the only thing the engine understands about any of it is that a frame has a
// length.
//
// Everything Go-specific stays inside the helper: which files make a unit, what
// the unit is called, and how to put one back.
func TestACacheCrossesAsBlobs(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	from := moduleCache(t)
	to := t.TempDir()

	store, err := blob.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	rt, err := helper.Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = rt.Close(ctx) }()

	h, err := goModHelper(t, ctx, rt)
	if err != nil {
		t.Fatal(err)
	}

	// The driver's side: every unit filed, and a map of what each is called.
	m, err := helper.Export(ctx, h, from, store)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	const key = "example.com/m@v1.2.3"

	if _, ok := m[key]; !ok {
		t.Fatalf("the map names %v, not %q", m, key)
	}

	if !store.Has(m[key]) {
		t.Fatal("the map names a blob the store does not hold")
	}

	// The worker's side: it holds nothing, asks for that key, and gets it.
	if got, err := helper.Index(ctx, h, to); err != nil || len(got) != 0 {
		t.Fatalf("a fresh cache indexed as %v (%v)", got, err)
	}

	if err := helper.Import(ctx, h, to, store, m, []string{key}); err != nil {
		t.Fatalf("import: %v", err)
	}

	got, err := helper.Index(ctx, h, to)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0] != key {
		t.Fatalf("after importing one unit the cache holds %v", got)
	}

	// And the bytes are the bytes, not merely the shape.
	at := filepath.Join(to, "cache", "download", "example.com", "m", "@v", "v1.2.3.mod")

	b, err := os.ReadFile(at) //nolint:gosec // a path this test built
	if err != nil {
		t.Fatalf("the unit arrived without its files: %v", err)
	}

	if string(b) != "module example.com/m\n" {
		t.Errorf("the imported file says %q", b)
	}
}

// A key nobody holds is skipped rather than fatal.
//
// A map may name a blob this machine never fetched, and a cache short of one
// unit is a cache - where a failed step is a failed build (I11).
func TestAnAbsentUnitIsSkipped(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	store, err := blob.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	rt, err := helper.Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = rt.Close(ctx) }()

	h, err := goModHelper(t, ctx, rt)
	if err != nil {
		t.Fatal(err)
	}

	to := moduleCache(t)

	m, err := helper.Export(ctx, h, to, store)
	if err != nil {
		t.Fatal(err)
	}

	// A key the map does not have, and one it does but the store does not.
	if err := helper.Import(ctx, h, to, store, m,
		[]string{"example.com/absent@v9.9.9", "example.com/m@v1.2.3"}); err != nil {
		t.Errorf("importing past an absent unit failed the whole batch: %v", err)
	}
}

// moduleCache is a directory the go-mod helper recognises, holding one module.
func moduleCache(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	at := filepath.Join(root, "cache", "download", "example.com", "m", "@v")

	if err := os.MkdirAll(at, 0o750); err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{
		"v1.2.3.info": `{"Version":"v1.2.3"}`,
		"v1.2.3.mod":  "module example.com/m\n",
		"v1.2.3.zip":  "not really a zip, but bytes are bytes",
	} {
		if err := os.WriteFile(filepath.Join(at, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return root
}
