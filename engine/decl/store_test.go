package decl_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/decl"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A declaration is filed under its own identity and read back by it.
func TestADeclarationIsFiledUnderItsIdentity(t *testing.T) {
	t.Parallel()

	store := t.TempDir()

	id, err := decl.Write(store, full())
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	if id != decl.ID(full()) {
		t.Errorf("filed under %v, want %v", id, decl.ID(full()))
	}

	if !decl.Has(store, id) {
		t.Error("the store does not admit holding what it just wrote")
	}

	got, ok, err := decl.Read(store, id)
	if err != nil || !ok {
		t.Fatalf("read: %v, held=%v", err, ok)
	}

	if strings.Join(got.Env, ",") != strings.Join(full().Env, ",") {
		t.Errorf("read back %+v", got)
	}
}

// It is beside the layer, never inside it.
//
// A layer is named by its content, so a file added inside the tree is a layer
// that is no longer what it says it is - and it would appear in the step's
// filesystem, which a declaration must never do (§3.2a).
func TestADeclarationIsNotInsideTheLayer(t *testing.T) {
	t.Parallel()

	store := t.TempDir()

	id, err := decl.Write(store, full())
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	tree := filepath.Join(store, "layers", id.String())
	entries, err := os.ReadDir(tree)
	if err == nil {
		t.Errorf("a directory exists at the layer path holding %d entries", len(entries))
	}

	_, err = os.Stat(decl.Path(store, id))
	if err != nil {
		t.Errorf("no declaration at the path this store names: %v", err)
	}
}

// An identity the store does not hold is an absence, not an error.
//
// The caller's next move is to fetch it, which is the right move whatever the
// reason - the same shape every lookup in this engine takes (§4.3).
func TestAnAbsentDeclarationIsAnAbsence(t *testing.T) {
	t.Parallel()

	_, ok, err := decl.Read(t.TempDir(), ir.NodeID{})
	if err != nil {
		t.Errorf("an absent declaration was an error: %v", err)
	}

	if ok {
		t.Error("a store claimed to hold a declaration nobody wrote")
	}
}

// Damage is an error, not an absence.
//
// A file that is there and cannot be read is not the same as one that is not
// there: treating it as a miss would silently drop what an image declares, which
// is the failure this whole mechanism exists to stop.
func TestADamagedDeclarationIsAnError(t *testing.T) {
	t.Parallel()

	store := t.TempDir()

	id, err := decl.Write(store, full())
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	err = os.WriteFile(decl.Path(store, id), []byte("not a declaration"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = decl.Read(store, id)
	if err == nil {
		t.Error("a damaged declaration read back as though it were fine")
	}
}
