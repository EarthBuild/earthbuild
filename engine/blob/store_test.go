package blob_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/blob"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

func newStore(t *testing.T) *blob.Store {
	t.Helper()

	s, err := blob.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	return s
}

// TestRoundTrip is the baseline: what goes in comes out.
func TestRoundTrip(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	want := []byte("step output\n")

	id, n, err := s.Put(bytes.NewReader(want))
	if err != nil {
		t.Fatal(err)
	}

	if n != int64(len(want)) {
		t.Errorf("Put reported %d bytes, want %d", n, len(want))
	}

	got, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestCorruptionIsDetected is invariant I2, and the reason 𝔅 cannot be poisoned:
// bytes that do not hash to the digest naming them are refused.
//
// This is enforcement level 2 - the verification *is* the mechanism, not an
// optional check that could be switched off - so the test corrupts the store
// behind the API's back, exactly as a hostile or failing disk would.
func TestCorruptionIsDetected(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	s, err := blob.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	id, _, err := s.Put(strings.NewReader("the real bytes"))
	if err != nil {
		t.Fatal(err)
	}

	// Rewrite the file on disk, keeping its name.
	h := id.String()
	err = os.WriteFile(filepath.Join(dir, h[:2], h), []byte("substituted!!!"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(id)
	if !errors.Is(err, blob.ErrCorrupt) {
		t.Fatalf("substituted bytes were not detected: err=%v", err)
	}

	if got != nil {
		t.Error("corrupt read returned bytes; it must return none")
	}
}

// TestVerificationPrecedesReturn checks that no byte of a corrupt blob reaches
// the caller.
//
// A streaming verifier detects corruption only at the end, by which point a
// caller materialising a layer has already written bad files to disk. The store
// therefore verifies whole before returning anything, and this test would fail
// if that were ever relaxed for speed.
func TestVerificationPrecedesReturn(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	s, err := blob.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	// A blob large enough that a streaming implementation would have handed
	// over most of it before noticing.
	big := bytes.Repeat([]byte("abcdefgh"), 1<<16)

	id, _, err := s.Put(bytes.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}

	corrupt := append([]byte(nil), big...)
	corrupt[len(corrupt)-1] ^= 0xff // flip one bit, in the last byte

	h := id.String()
	err = os.WriteFile(filepath.Join(dir, h[:2], h), corrupt, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(id)
	if err == nil || got != nil {
		t.Fatal("a single flipped bit in the last byte was not caught before return")
	}
}

// TestPutIsIdempotent checks invariant I9: state is insert-or-remove, never
// modify in place. Storing the same content twice must not rewrite the file,
// because a concurrent reader holding that digest is entitled to assume the
// bytes behind it are stable.
func TestPutIsIdempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	s, err := blob.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	id, _, err := s.Put(strings.NewReader("stable"))
	if err != nil {
		t.Fatal(err)
	}

	h := id.String()
	path := filepath.Join(dir, h[:2], h)

	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	id2, _, err := s.Put(strings.NewReader("stable"))
	if err != nil {
		t.Fatal(err)
	}

	if id2 != id {
		t.Fatal("identical content produced different digests")
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("re-storing identical content rewrote the file; I9 requires insert-or-remove")
	}
}

// TestConcurrentPutsAreSafe checks that many writers storing the same and
// different content do not corrupt each other. Writes go to a temporary file
// and are renamed, so the only observable states are absent and complete.
func TestConcurrentPutsAreSafe(t *testing.T) {
	t.Parallel()

	s := newStore(t)

	const writers = 32

	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		ids = map[ir.NodeID]bool{}
	)

	for i := range writers {
		wg.Go(func() {
			// Half write identical content, half unique.
			content := "shared"
			if i%2 == 1 {
				// Formatted rather than cast: `rune('a'+i)` is an int
				// conversion that would wrap into nonsense for a large i, and
				// nothing here bounds i (gosec G115).
				content = fmt.Sprintf("unique-%d", i)
			}

			id, _, err := s.Put(strings.NewReader(content))
			if err != nil {
				t.Error(err)

				return
			}

			mu.Lock()
			ids[id] = true
			mu.Unlock()
		})
	}

	wg.Wait()

	for id := range ids {
		_, err := s.Get(id)
		if err != nil {
			t.Errorf("blob %s unreadable after concurrent writes: %v", id, err)
		}
	}
}

// TestMissingBlobIsNotFound checks that absence is reported as absence, so a
// caller can treat it as a miss rather than a failure.
func TestMissingBlobIsNotFound(t *testing.T) {
	t.Parallel()

	s := newStore(t)

	var absent ir.NodeID

	absent[0] = 0xde

	_, err := s.Get(absent)
	if !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("missing blob reported as %v, want ErrNotFound", err)
	}

	if s.Has(absent) {
		t.Error("Has reported a blob that was never stored")
	}
}

// TestDeleteRemoves checks the only sanctioned way for a blob to leave: garbage
// collection removes entries and never rewrites them.
func TestDeleteRemoves(t *testing.T) {
	t.Parallel()

	s := newStore(t)

	id, _, err := s.Put(strings.NewReader("transient"))
	if err != nil {
		t.Fatal(err)
	}

	err = s.Delete(id)
	if err != nil {
		t.Fatal(err)
	}

	if s.Has(id) {
		t.Error("blob survived deletion")
	}

	// Deleting twice is not an error: GC must be re-runnable.
	err = s.Delete(id)
	if err != nil {
		t.Errorf("second delete failed: %v", err)
	}
}
