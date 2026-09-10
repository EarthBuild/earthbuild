package store_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// stackOf is a stack of one made-up layer, which is all these need: the memo
// keys on the ids and never opens them.
func stackOf(b byte) []ir.NodeID {
	var id ir.NodeID
	id[0] = b

	return []ir.NodeID{id}
}

// TestAnUnchangedDestinationIsCurrent is the export a build does not have to do.
//
// `SAVE ARTIFACT AS LOCAL` of a 70 MiB binary costs 0.409s on a microVM - stage
// 0.089, fetch 0.242 out of the guest, copy out 0.078 - on every build,
// including one where all 94 steps hit the cache. The bytes are already on this
// machine, at the destination, put there by the build before.
func TestAnUnchangedDestinationIsCurrent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	memo := store.OpenExportMemo(root)
	dest := filepath.Join(t.TempDir(), "earthly")

	err := os.WriteFile(dest, []byte("a binary"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	stack := stackOf(1)

	if memo.Current(stack, "/build/earthly", dest) {
		t.Fatal("current before anything was noted")
	}

	memo.NoteOutput(stack, "/build/earthly", dest)

	if !memo.Current(stack, "/build/earthly", dest) {
		t.Fatal("not current immediately after being noted")
	}
}

// TestADifferentStackIsNotCurrent. The key is the stack and the path, and a
// stack is a list of content-addressed layers - so different bytes are a
// different key and cannot collide with this answer.
func TestADifferentStackIsNotCurrent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	memo := store.OpenExportMemo(root)
	dest := filepath.Join(t.TempDir(), "earthly")

	err := os.WriteFile(dest, []byte("a binary"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	memo.NoteOutput(stackOf(1), "/build/earthly", dest)

	if memo.Current(stackOf(2), "/build/earthly", dest) {
		t.Fatal("a different stack read as current")
	}

	if memo.Current(stackOf(1), "/build/other", dest) {
		t.Fatal("a different artifact read as current")
	}
}

// TestATouchedDestinationIsNotCurrent.
//
// **The memo is about what is on this machine, not only about what was built.**
// Somebody who edits or deletes the exported file has to get it back, and the
// stack cannot tell anyone that happened - so the destination is stat'd and its
// size and modification time have to be the ones this store wrote.
func TestATouchedDestinationIsNotCurrent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	memo := store.OpenExportMemo(root)
	dest := filepath.Join(t.TempDir(), "earthly")
	stack := stackOf(1)

	for _, c := range []struct {
		what string
		do   func()
	}{
		{"rewritten with different bytes", func() {
			_ = os.WriteFile(dest, []byte("a different binary"), 0o600)
		}},
		{"touched", func() {
			at := time.Now().Add(time.Hour)
			_ = os.Chtimes(dest, at, at)
		}},
		{"removed", func() { _ = os.Remove(dest) }},
	} {
		err := os.WriteFile(dest, []byte("a binary"), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		memo.NoteOutput(stack, "/build/earthly", dest)

		if !memo.Current(stack, "/build/earthly", dest) {
			t.Fatalf("%s: not current before the change", c.what)
		}

		c.do()

		if memo.Current(stack, "/build/earthly", dest) {
			t.Errorf("a destination %s still read as current", c.what)
		}
	}
}

// TestNoStoreRemembersNothing. The zero memo is the honest answer for a caller
// with no store, and must not resolve against the working directory.
func TestNoStoreRemembersNothing(t *testing.T) {
	t.Parallel()

	memo := store.OpenExportMemo("")
	dest := filepath.Join(t.TempDir(), "earthly")

	err := os.WriteFile(dest, []byte("a binary"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	memo.NoteOutput(stackOf(1), "/build/earthly", dest)

	if memo.Current(stackOf(1), "/build/earthly", dest) {
		t.Fatal("a memo with no store answered")
	}
}
