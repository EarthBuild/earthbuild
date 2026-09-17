package cacheshare_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/blob"
	"github.com/EarthBuild/earthbuild/engine/cacheshare"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A pinned helper is found without a build directory, which is the worker's
// case and the only case that matters.
//
// **A worker has no Earthfile.** `--helper ./go.wasm` names a file on the
// machine that read it and nothing here, so the path is not a route a worker
// has - the digest is. This proves the store is consulted by getting past the
// point where a missing file would have stopped it: the bytes are found and
// rejected as a module, which is a complaint only something holding them can
// make.
func TestAPinnedHelperIsFoundWithNoBuildDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := put(t, root, []byte("not a wasm module, but bytes all the same"))

	err := offer(t, root, ir.Mount{
		ID: "k", Portable: true, Helper: "./go.wasm", HelperID: id.String(),
	})
	if err == nil {
		t.Fatal("garbage compiled as a helper module")
	}

	if strings.Contains(err.Error(), "read the helper") {
		t.Errorf("a pinned helper was looked for on disk: %v"+
			"\n  a worker has no build directory, so that route does not exist there", err)
	}

	if !strings.Contains(err.Error(), "compile helper") {
		t.Errorf("the failure does not name compilation, so it is not clear the"+
			" module was read out of the store at all: %v", err)
	}
}

// An unpinned helper is not a route a worker has, and the refusal says which
// helper (I10).
//
// This is not a regression: it is the case the pin exists for. A build planned
// without a helper resolver keys as written and shares nothing on the far end,
// which is a slower build elsewhere and never a wrong one.
func TestAnUnpinnedHelperCannotBeFoundByAWorker(t *testing.T) {
	t.Parallel()

	err := offer(t, t.TempDir(), ir.Mount{ID: "k", Portable: true, Helper: "./go.wasm"})
	if err == nil {
		t.Fatal("a worker found a helper at a path only the driver has")
	}

	if !strings.Contains(err.Error(), "./go.wasm") {
		t.Errorf("the refusal does not name the helper: %v", err)
	}
}

// A pin naming nothing this store holds says so, rather than reading a path
// that happens to exist.
//
// The two are not interchangeable. A digest names bytes and a path names
// whatever is there now, so quietly substituting one for the other would run a
// different helper than the step was keyed under - which is the substitution the
// pin was added to prevent.
func TestAPinThisStoreLacksIsNotSubstituted(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	missing := ir.DigestOf([]byte("never filed")).String()

	err := offer(t, root, ir.Mount{ID: "k", Portable: true, HelperID: missing})
	if err == nil {
		t.Fatal("a helper nobody holds was run")
	}

	if !strings.Contains(err.Error(), missing) {
		t.Errorf("the refusal does not name the pin it could not find: %v", err)
	}
}

// A cache mount the step never wrote is not a failure.
func TestAnAbsentCacheDirectoryIsNothingToShare(t *testing.T) {
	t.Parallel()

	s := cacheshare.New(t.TempDir(), "", nil)

	err := s.Offer(context.Background(),
		ir.Mount{ID: "k", Portable: true, Helper: "./go.wasm"},
		filepath.Join(t.TempDir(), "never-made"), "")
	if err != nil {
		t.Errorf("a cache the step never wrote reported %v, want nothing to do", err)
	}
}

// offer runs Offer against a directory that exists, with no build directory -
// the worker's configuration exactly.
func offer(t *testing.T, root string, m ir.Mount) error {
	t.Helper()

	return cacheshare.New(root, "", nil).Offer(context.Background(), m, t.TempDir(), "")
}

func put(t *testing.T, root string, body []byte) ir.NodeID {
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
