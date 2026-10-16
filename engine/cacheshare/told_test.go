package cacheshare_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/blob"
	"github.com/EarthBuild/earthbuild/engine/cacheshare"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// What this machine has filed is what it can tell another machine about.
//
// **The driver's half of the join.** A map names a cache's units and is itself a
// blob; the pointer from a cache to its latest map is a mutable file and is
// therefore the one thing here that is not content-addressed, so a worker cannot
// derive it and has to be told.
//
// Keyed exactly as the pointer is - `<id>/<scope>` - so the scope, and the trust
// domain inside it, travels without either end comparing domains.
func TestAMachineCanSayWhatItHasFiled(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := ir.DigestOf([]byte("a map"))
	file(t, filepath.Join(root, "cachemaps", "go-build", "abc123"), id.String())

	got := cacheshare.New(root, "", nil).Known()

	if len(got) != 1 || got["go-build/abc123"] != id.String() {
		t.Errorf("filed maps read as %v"+
			"\n  want one entry keyed go-build/abc123, which is what the pointer"+
			" is keyed by and what a worker will look up", got)
	}
}

// A machine that has filed nothing says nothing, rather than failing.
func TestAMachineThatHasFiledNothingSaysNothing(t *testing.T) {
	t.Parallel()

	if got := cacheshare.New(t.TempDir(), "", nil).Known(); len(got) != 0 {
		t.Errorf("a store with no cachemaps directory reported %v", got)
	}
}

// A worker stocks from the map it was told about, holding no pointer of its own.
//
// This is the case the whole hint exists for: a machine that has never filled
// this cache has no pointer, so without being told there is nothing for it to
// look up and it fills the cache by doing the work.
func TestAWorkerStocksFromWhatItWasTold(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := putBlob(t, root, []byte("left-pad@1\t"+ir.DigestOf([]byte("a unit")).String()+"\n"))

	s := cacheshare.New(root, "", nil)
	s.Told(func(key string) (string, bool) {
		if key == "go-build/abc123" {
			return id.String(), true
		}

		return "", false
	})

	// No helper can be found, so this gets as far as looking the map up and no
	// further - which is exactly the step under test. Without the hint it
	// returns having had nothing to look up at all.
	err := s.Stock(context.Background(),
		ir.Mount{ID: "go-build", Portable: true, Helper: "./none.wasm"},
		filepath.Join(root, "mounts", "go-build", "abc123"))
	if err == nil {
		t.Error("a worker told which map describes this cache did not try to use it" +
			"\n  the hint was ignored, so a cold worker recompiles what a peer holds")
	}
}

// And a machine that was told nothing falls back to its own pointer.
func TestWithoutAHintTheLocalPointerIsUsed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := putBlob(t, root, []byte("left-pad@1\t"+ir.DigestOf([]byte("a unit")).String()+"\n"))
	file(t, filepath.Join(root, "cachemaps", "go-build", "abc123"), id.String())

	err := cacheshare.New(root, "", nil).Stock(context.Background(),
		ir.Mount{ID: "go-build", Portable: true, Helper: "./none.wasm"},
		filepath.Join(root, "mounts", "go-build", "abc123"))
	if err == nil {
		t.Error("a machine ignored the map it filed itself")
	}
}

func file(t *testing.T, at, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(at), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(at, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func putBlob(t *testing.T, root string, body []byte) ir.NodeID {
	t.Helper()

	st, err := blob.New(root)
	if err != nil {
		t.Fatal(err)
	}

	id, _, err := st.Put(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	return id
}
