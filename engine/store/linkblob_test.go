package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A declared output's bytes become fetchable without being copied.
//
// **A link, because the store is insert-only.** A committed layer is never
// rewritten (I9), so a node and the layer file it shares an inode with can
// never diverge - which is what makes this safe rather than clever. The two
// directories are in one store root and so on one filesystem, so the link is
// O(1) and no bytes move.
//
// Done for declared outputs when they are named, rather than when a client asks
// for them: with deferred materialisation most outputs are never fetched at
// all, and at the price of a link it is not worth knowing which.
func TestADeclaredOutputIsLinkedNotCopied(t *testing.T) {
	// Not parallel: SelectHashForTest changes a process-wide choice.
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	root := t.TempDir()
	st := store.DirStore(root)

	// A layer as `commit` leaves one: a directory of files under its digest.
	id := ir.DigestOf([]byte("a layer"))
	content := []byte("what the action produced\n")

	at := filepath.Join(store.LayerStore(root).Path(id), "out", "a.txt")
	if err := os.MkdirAll(filepath.Dir(at), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(at, content, 0o600); err != nil {
		t.Fatal(err)
	}

	blob := ir.DigestOf(content)

	if err := st.LinkBlob(id, "out/a.txt", blob); err != nil {
		t.Fatal(err)
	}

	// Fetchable by digest, and verified on the way out as everything is.
	got, err := st.Node(blob)
	if err != nil {
		t.Fatalf("the output was named and cannot be fetched: %v", err)
	}

	if string(got) != string(content) {
		t.Errorf("fetched %q, and the action produced %q", got, content)
	}

	// **One inode, not two.** A copy would work and would cost the output's
	// size on every action, which for a build's real artefacts is the whole
	// point of not doing it.
	a, err := os.Stat(at)
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.Stat(store.NodePath(root, blob))
	if err != nil {
		t.Fatal(err)
	}

	if !os.SameFile(a, b) {
		t.Error("the node is a copy of the layer's file rather than a link to it")
	}
}

// Linking something whose bytes are not what it is called is refused.
//
// The store's one rule: a blob is named by its contents, and a link that put a
// file under the wrong name would be a store that lies on every later read.
func TestALinkIsCheckedAgainstItsName(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	root := t.TempDir()
	st := store.DirStore(root)

	id := ir.DigestOf([]byte("a layer"))

	at := filepath.Join(store.LayerStore(root).Path(id), "a.txt")
	if err := os.MkdirAll(filepath.Dir(at), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(at, []byte("these bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	wrong := ir.DigestOf([]byte("but this name"))

	if err := st.LinkBlob(id, "a.txt", wrong); err == nil {
		t.Error("a file was filed under a name its contents do not produce")
	}

	if _, err := os.Stat(store.NodePath(root, wrong)); err == nil {
		t.Error("the refused link was left behind in the store")
	}
}
