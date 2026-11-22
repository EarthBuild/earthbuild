package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A tree sent to this store comes back as a filesystem.
//
// **The round trip an execution service is.** A client walks its input root into
// Directory messages and file blobs and sends them; this turns them back into
// the tree the client described. If the two disagree the action runs over
// something nobody asked for.
func TestAnInputRootBecomesTheTreeItDescribes(t *testing.T) {
	// Not parallel: SelectHashForTest changes a process-wide choice, and what
	// it races against is every other test that hashes anything - which in this
	// package is most of them.
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	root := t.TempDir()
	st := store.DirStore(root)

	// The tree a client would send, built the way this engine builds one.
	src := t.TempDir()
	writeFiles(t, src, map[string]string{
		"main.go":     "package main",
		"run.sh":      "#!/bin/sh\necho hi",
		"sub/dep.go":  "package sub",
		"sub/x/y.txt": "deep",
	})

	if err := os.Chmod(filepath.Join(src, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	m, err := layer.Manifest(src)
	if err != nil {
		t.Fatal(err)
	}

	f := layer.NewFold()
	if !f.Add(m) {
		t.Fatal("the manifest did not fold")
	}

	tree := f.Tree()
	if err := st.NoteNodes(tree); err != nil {
		t.Fatal(err)
	}

	// The file blobs a client uploads alongside the tree.
	for p, body := range map[string]string{
		"main.go": "package main", "run.sh": "#!/bin/sh\necho hi",
		"sub/dep.go": "package sub", "sub/x/y.txt": "deep",
	} {
		if err := st.Accept(ir.DigestOf([]byte(body)), []byte(body)); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}

	into := filepath.Join(t.TempDir(), "work")
	if err := st.Materialise(tree.Root(), into); err != nil {
		t.Fatal(err)
	}

	for p, want := range map[string]string{
		"main.go": "package main", "run.sh": "#!/bin/sh\necho hi",
		"sub/dep.go": "package sub", "sub/x/y.txt": "deep",
	} {
		got, err := os.ReadFile(filepath.Join(into, p))
		if err != nil {
			t.Errorf("%s: %v", p, err)

			continue
		}

		if string(got) != want {
			t.Errorf("%s holds %q, want %q", p, got, want)
		}
	}

	// The executable bit survives, because whether a script may be run is the
	// difference between an action working and an action not.
	fi, err := os.Stat(filepath.Join(into, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}

	if fi.Mode()&0o111 == 0 {
		t.Errorf("run.sh came back as %v, and an action cannot run it", fi.Mode())
	}
}

// An input root with a blob missing is refused, not half-written.
//
// **A hole in an input root is undetectable downstream.** The action runs, the
// compiler reports a file it cannot find or quietly compiles less, and the
// result is cached under a key that says the inputs were complete.
func TestAnIncompleteInputRootIsRefused(t *testing.T) {
	// Not parallel: SelectHashForTest changes a process-wide choice, and what
	// it races against is every other test that hashes anything - which in this
	// package is most of them.
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	st := store.DirStore(t.TempDir())

	src := t.TempDir()
	writeFiles(t, src, map[string]string{"a.txt": "one"})

	m, err := layer.Manifest(src)
	if err != nil {
		t.Fatal(err)
	}

	f := layer.NewFold()
	if !f.Add(m) {
		t.Fatal("did not fold")
	}

	tree := f.Tree()
	if err := st.NoteNodes(tree); err != nil {
		t.Fatal(err)
	}

	// The tree is there and the file it names is not.
	err = st.Materialise(tree.Root(), filepath.Join(t.TempDir(), "work"))
	if err == nil {
		t.Fatal("an input root naming a blob this store does not hold was" +
			" materialised anyway")
	}

	// And it says what to do about it.
	if !contains(err.Error(), "FindMissingBlobs") {
		t.Errorf("the refusal does not tell the client how to fix it:\n  %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}

	return false
}
