package layer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// treeFor writes a small tree with a subdirectory, a symlink and files of
// differing sizes - enough shapes that a capture which ignored one of them
// would not go unnoticed.
func treeFor(t *testing.T) (string, map[string]ir.NodeID) {
	t.Helper()

	root := t.TempDir()

	files := map[string]string{
		"a.txt":       "the first file",
		"b.bin":       string(make([]byte, 4096)),
		"sub/c.txt":   "nested",
		"sub/d.empty": "",
	}

	known := map[string]ir.NodeID{}

	for name, body := range files {
		at := filepath.Join(root, filepath.FromSlash(name))

		err := os.MkdirAll(filepath.Dir(at), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(at, []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		h := ir.NewHasher()
		_, _ = h.Write([]byte(body))
		known[name] = h.Sum()
	}

	err := os.Symlink("a.txt", filepath.Join(root, "link"))
	if err != nil {
		t.Fatal(err)
	}

	return root, known
}

// **The two ways of asking must give the same answer, or the store files a
// layer under a name it cannot reproduce.**
//
// `fillContents` reads every file back to digest it - 0.958s of a cold
// `golang:1.26-alpine` FROM, re-reading bytes the unpacker had just written and
// could have hashed for nothing. Supplying them is only safe if the id is
// identical either way, which is what this asserts: green paper I3 says a hit
// is never false, and two definitions of a layer's content is exactly how that
// stops being true.
func TestASuppliedDigestGivesTheSameIdentityAsReadingTheFile(t *testing.T) {
	t.Parallel()

	root, known := treeFor(t)

	read, err := layer.TakeOwnedIn(root, layer.IDMap{}, layer.IDMap{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	told, err := layer.TakeOwnedKnowing(root, layer.IDMap{}, layer.IDMap{}, nil, known)
	if err != nil {
		t.Fatal(err)
	}

	if told.ID != read.ID {
		t.Fatalf("the same tree captured as %v when read and %v when told\n"+
			"  a layer whose identity depends on how it was measured is a layer\n"+
			"  the store cannot find again", read.ID, told.ID)
	}
}

// TestAPathNotSuppliedIsStillRead: the map is a shortcut, never a definition of
// what the tree contains. A layer only partly known has to come out the same.
func TestAPathNotSuppliedIsStillRead(t *testing.T) {
	t.Parallel()

	root, known := treeFor(t)

	read, err := layer.TakeOwnedIn(root, layer.IDMap{}, layer.IDMap{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	delete(known, "sub/c.txt")
	delete(known, "b.bin")

	told, err := layer.TakeOwnedKnowing(root, layer.IDMap{}, layer.IDMap{}, nil, known)
	if err != nil {
		t.Fatal(err)
	}

	if told.ID != read.ID {
		t.Fatalf("a partly supplied capture came out as %v, want %v", told.ID, read.ID)
	}
}

// TestAnEmptyKnowledgeIsJustTheOrdinaryWalk: the fallback must not be a
// different code path that merely happens to agree today.
func TestAnEmptyKnowledgeIsJustTheOrdinaryWalk(t *testing.T) {
	t.Parallel()

	root, _ := treeFor(t)

	read, err := layer.TakeOwnedIn(root, layer.IDMap{}, layer.IDMap{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, known := range []map[string]ir.NodeID{nil, {}} {
		told, terr := layer.TakeOwnedKnowing(root, layer.IDMap{}, layer.IDMap{}, nil, known)
		if terr != nil {
			t.Fatal(terr)
		}

		if told.ID != read.ID {
			t.Errorf("knowing nothing captured as %v, want %v", told.ID, read.ID)
		}
	}
}
