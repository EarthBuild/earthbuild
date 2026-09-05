package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// The store is a thing with a surface, not a path everybody knows.
//
// Six capabilities reach into it host-side, each joining a path and walking. A
// store on a block device cannot be walked from the host at all, so each has to
// become an operation before the storage can move - and naming them is what
// makes that reviewable rather than a rewrite (E541).
//
// This test is the surface. A directory-backed store satisfies it today; a
// guest-backed one satisfies it in phase 2, and nothing above it has to know
// which it is holding.
func TestADirectoryStoreSatisfiesTheSurface(t *testing.T) {
	t.Parallel()

	var s core.Store = DirStore(t.TempDir())

	id := ir.NodeID{1, 2, 3}

	if s.Has(id) {
		t.Error("an empty store claims to hold a layer")
	}

	// A store says where it keeps a layer, because a caller that materialises
	// one still needs the path - and phase 2 is where that stops being true.
	if s.LayerPath(id) == "" {
		t.Error("the store cannot say where a layer lives")
	}
}

// What an image declares is asked of the store, not read from beside a file.
func TestTheStoreAnswersWhatAnImageDeclared(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := DirStore(root)
	id := ir.NodeID{9}

	at := filepath.Join(root, "layers", id.String())
	err := os.MkdirAll(at, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	if got := s.Declaration(id); got != (ir.NodeID{}) {
		t.Errorf("a layer with no configuration declared %v", got)
	}

	b, err := json.Marshal(ocispec.ImageConfig{Env: []string{"PATH=/go/bin"}})
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(at+ConfigSuffix, b, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if got := s.Declaration(id); got == (ir.NodeID{}) {
		t.Error("a layer whose image declared an environment produced no declaration")
	}
}

// A captured tree is filed by asking the store, not by renaming into it.
//
// `placeCaptured` took a store path and moved a directory under it, which is the
// shape that cannot survive the store becoming a disk: the host has no path to
// rename into. Asking the store to take the tree keeps the contract - the name
// is the digest of what arrives, never the caller's choice - and leaves
// somewhere for phase 2 to put a protocol.
func TestTheStoreTakesACapturedTree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := DirStore(root)

	// Two trees with the same contents *and the same timestamps*. Not the same
	// contents alone: a layer's identity includes mtime to nanosecond precision
	// (I8), so two trees built a moment apart differ legitimately - which is why
	// Content exists for comparing runs and Layer does not.
	when := time.Unix(1000000, 0)

	stage := func(name string) string {
		t.Helper()

		at := filepath.Join(root, "layers", name)
		err := os.MkdirAll(at, 0o750)
		if err != nil {
			t.Fatal(err)
		}

		f := filepath.Join(at, "a")
		err = os.WriteFile(f, []byte("hello"), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		for _, p := range []string{f, at} {
			err := os.Chtimes(p, when, when)
			if err != nil {
				t.Fatal(err)
			}
		}

		return at
	}

	first, second := stage(".incoming"), stage(".incoming2")

	id, err := s.Place(first)
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	if id == (ir.NodeID{}) {
		t.Fatal("a placed tree got no identity")
	}

	if !s.Has(id) {
		t.Error("the store does not hold what it just filed")
	}

	twice, err := s.Place(second)
	if err != nil {
		t.Fatalf("place again: %v", err)
	}

	if twice != id {
		t.Errorf("identical trees filed as %v and %v", id, twice)
	}
}

// A layer with a chosen name arrives whole or not at all.
//
// A local context is named by the node that asked for it rather than by a digest
// of its contents, so it cannot go through Place. It was therefore built
// directly under its final name - and a copy that failed half way left a
// directory that `Has` reports as present, which is a base a later build would
// stand on. The rule everywhere else in this engine is that a transfer leaving
// nothing is better than one leaving half (see fleet.Put); this brings the
// context path under it.
func TestANamedLayerArrivesWholeOrNotAtAll(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := DirStore(root)
	id := ir.NodeID{7, 7}

	staging, err := s.Staging(".ctx-")
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(staging, "a"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// Nothing is visible under the name while it is being built.
	if s.Has(id) {
		t.Error("the store holds a layer nobody has committed")
	}

	err = s.PutNamed(id, staging)
	if err != nil {
		t.Fatalf("put named: %v", err)
	}

	if !s.Has(id) {
		t.Error("a committed layer is not there")
	}

	// Committing again is not an error: two builds may produce the same context.
	second, err := s.Staging(".ctx-")
	if err != nil {
		t.Fatal(err)
	}

	err = s.PutNamed(id, second)
	if err != nil {
		t.Errorf("committing the same name twice: %v", err)
	}
}
