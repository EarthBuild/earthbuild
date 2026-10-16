package remote_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/remote"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// oneEntry answers for a single key, which is all a front end needs of a cache.
type oneEntry struct {
	key core.Key
	e   core.Entry
}

func (o oneEntry) Get(k core.Key) (core.Entry, bool) {
	if k != o.key {
		return core.Entry{}, false
	}

	return o.e, true
}

// A result this engine recorded is an ActionResult a client can read.
//
// **The key is the Action digest**, so the number a client asks under is the
// number this engine derived - no index between them, and no second place for
// the two to disagree.
func TestAResultIsServedAsAnActionResult(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	root := t.TempDir()
	st := store.DirStore(root)

	// A layer in the store, with its manifest beside it.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "out.txt"), []byte("built"), 0o600); err != nil {
		t.Fatal(err)
	}

	took, err := layer.Take(dir)
	if err != nil {
		t.Fatal(err)
	}

	m, err := layer.Manifest(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(store.LayerStore(root).Path(took.ID), 0o750); err != nil {
		t.Fatal(err)
	}

	store.NoteManifest(root, took.ID, m)

	key := core.Key{0xab, 0xcd}
	srv := httptest.NewServer(&remote.Cache{
		Store:   st,
		Actions: oneEntry{key: key, e: core.Entry{Layer: took.ID, Content: took.Content, Exit: 0}},
	})

	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ac/" + ir.NodeID(key).String())
	if err != nil {
		t.Fatal(err)
	}

	body := readAll(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/ac answered %s for a key the cache holds", resp.Status)
	}

	// The result must name the tree the layer materialises to, and that
	// Directory must now be fetchable - a result pointing at something the CAS
	// cannot hand over is a hit a client cannot use.
	if len(body) == 0 {
		t.Fatal("an empty ActionResult")
	}

	casResp, err := http.Get(srv.URL + "/cas/" + took.Content.String())
	if err != nil {
		t.Fatal(err)
	}

	blob := readAll(t, casResp)

	if casResp.StatusCode != http.StatusOK {
		t.Errorf("the result names %v and the CAS answered %s"+
			"\n  a hit whose tree cannot be fetched is a hit a client cannot use",
			took.Content, casResp.Status)
	}

	if ir.DigestOf(blob) != took.Content {
		t.Errorf("the CAS served bytes naming %v under %v", ir.DigestOf(blob), took.Content)
	}
}

// A key the cache does not hold is a miss.
func TestAnUnknownActionIsAMiss(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	srv := httptest.NewServer(&remote.Cache{
		Store:   store.DirStore(t.TempDir()),
		Actions: oneEntry{key: core.Key{1}},
	})

	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ac/" + ir.NodeID{2}.String())
	if err != nil {
		t.Fatal(err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("an unknown action answered %s, want 404", resp.Status)
	}
}

// An entry whose layer does not fold to what it claims is not served.
//
// **The one thing a content-addressed store may never do.** If the manifest
// folds to a different tree than the entry recorded, then the entry describes
// something this store cannot produce - and handing back a different tree under
// the client's name would be worse than answering nothing.
func TestAResultThatDoesNotMatchItsLayerIsNotServed(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	root := t.TempDir()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "out.txt"), []byte("built"), 0o600); err != nil {
		t.Fatal(err)
	}

	took, err := layer.Take(dir)
	if err != nil {
		t.Fatal(err)
	}

	m, err := layer.Manifest(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(store.LayerStore(root).Path(took.ID), 0o750); err != nil {
		t.Fatal(err)
	}

	store.NoteManifest(root, took.ID, m)

	key := core.Key{0xab}
	srv := httptest.NewServer(&remote.Cache{
		Store: store.DirStore(root),
		// Content says one thing; the layer folds to another.
		Actions: oneEntry{key: key, e: core.Entry{Layer: took.ID, Content: ir.NodeID{0xff}}},
	})

	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ac/" + ir.NodeID(key).String())
	if err != nil {
		t.Fatal(err)
	}

	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		t.Error("a result naming a tree its layer does not fold to was served")
	}
}
