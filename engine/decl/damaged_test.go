package decl_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"

	"github.com/EarthBuild/earthbuild/engine/decl"
)

// A declaration that cannot be decoded is removed, and still reported.
//
// **Both, because they answer different questions.** The removal is what makes
// the *next* build work: a declaration is named by its contents, so nothing in
// it is irreplaceable and whatever wrote it writes it again. The message already
// said "it is safe to delete and will be fetched again" - and then left the file
// there, so every build after it failed identically and the store could only be
// repaired by hand.
//
// The error is what makes *this* build say why. Reporting the damage as a plain
// absence would heal the store and hand the reader the materialiser's vaguer
// complaint - "this store holds neither a layer nor a declaration" - for a fault
// that had a precise name a moment earlier.
//
// **Removing the file is not the same as healing the store**, which was worth
// measuring rather than assuming: if the element's layer is still present, the
// step that would have re-filed the declaration takes a cache hit and re-files
// nothing, and the build after this one fails on the absence instead. The
// message says so rather than promising a recovery it cannot make.
func TestADamagedDeclarationIsRemovedAndStillReported(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	id := writeThenDamage(t, store)

	_, held, err := decl.Read(store, id)
	if err == nil {
		t.Fatal("a damaged declaration was healed silently, so this build says nothing")
	}

	if held {
		t.Error("a damaged declaration was reported as held")
	}

	if _, statErr := os.Stat(decl.Path(store, id)); !os.IsNotExist(statErr) {
		t.Error("the damaged declaration is still there, so the next build fails the same way")
	}
}

// A good one is untouched, which is the case this must not break.
func TestAGoodDeclarationIsKept(t *testing.T) {
	t.Parallel()

	store := t.TempDir()

	id, err := decl.Write(store, decl.Declaration{Env: []string{"A=b"}})
	if err != nil {
		t.Fatal(err)
	}

	got, held, err := decl.Read(store, id)
	if err != nil || !held {
		t.Fatalf("a good declaration was not read back: held=%v err=%v", held, err)
	}

	if len(got.Env) != 1 || got.Env[0] != "A=b" {
		t.Errorf("the declaration came back as %+v", got)
	}

	if _, err := os.Stat(decl.Path(store, id)); err != nil {
		t.Errorf("a good declaration was removed: %v", err)
	}
}

// writeThenDamage files a real declaration and truncates it, which is what an
// unclean shutdown leaves behind: the rename landed and the bytes did not.
func writeThenDamage(t *testing.T, store string) ir.NodeID {
	t.Helper()

	written, err := decl.Write(store, decl.Declaration{Env: []string{"A=b"}})
	if err != nil {
		t.Fatal(err)
	}

	at := decl.Path(store, written)
	if !strings.HasSuffix(at, ".decl") {
		t.Fatalf("a declaration is at %s", at)
	}

	if err := os.Truncate(at, 0); err != nil {
		t.Fatal(err)
	}

	if filepath.Dir(at) == "" {
		t.Fatal("no directory")
	}

	return written
}
