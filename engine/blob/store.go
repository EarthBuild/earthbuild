// Package blob is 𝔅: the content-addressed blob store (green paper §2.1).
//
// Its defining property is equation 2.2 - every digest in the store hashes to
// the bytes it names - and the consequence is that 𝔅 cannot be poisoned. A
// store returning wrong bytes is detected on read, the read becomes a miss, and
// an attacker with total control of it can deny service and nothing else.
//
// This is the impure half of the engine and deliberately not in engine/core:
// it opens files, and core does not.
package blob

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// ErrCorrupt reports that stored bytes do not hash to the digest naming them.
// Callers treat it as absence, never as a fatal error: green paper I4's
// degrade-to-miss rule applies to every layer of the storage stack.
var ErrCorrupt = errors.New("blob does not match its digest")

// ErrNotFound reports that a digest is not in the store.
var ErrNotFound = errors.New("blob not found")

// Store is a filesystem-backed 𝔅.
//
// Blobs live at root/<first two hex>/<full hex>, sharded so a directory does
// not accumulate a hundred thousand entries.
type Store struct {
	root string
}

// New opens or creates a store at root.
func New(root string) (*Store, error) {
	err := os.MkdirAll(filepath.Join(root, "tmp"), 0o700)
	if err != nil {
		return nil, fmt.Errorf("create blob store: %w", err)
	}

	return &Store{root: root}, nil
}

func (s *Store) path(id ir.NodeID) string {
	h := id.String()

	return filepath.Join(s.root, h[:2], h)
}

// Has reports whether a digest is present.
//
// It does not verify: verification happens on read, where the bytes are
// available. A Has that lied would cost a wasted fetch, never a wrong result.
func (s *Store) Has(id ir.NodeID) bool {
	_, err := os.Stat(s.path(id))

	return err == nil
}

// Put stores the contents of r and returns the digest naming them.
//
// The digest is computed from the bytes, so a caller cannot choose it: this is
// what makes equation 2.2 hold by construction rather than by discipline.
//
// Writes are atomic - a temporary file, then a rename - and a blob that already
// exists is left alone rather than rewritten. State is insert-or-remove, never
// modify in place (invariant I9), which is what lets a concurrent reader hold a
// digest and be certain the bytes behind it will not change under them.
func (s *Store) Put(r io.Reader) (ir.NodeID, int64, error) {
	tmp, err := os.CreateTemp(filepath.Join(s.root, "tmp"), "blob-*")
	if err != nil {
		return ir.NodeID{}, 0, fmt.Errorf("create temp blob: %w", err)
	}

	defer func() {
		// Both are cleanup after the real work, and both are expected to fail
		// on the happy path: the file has been closed and renamed away. Ignored
		// explicitly, so the reader knows it was decided rather than forgotten.
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	h := ir.NewHasher()

	n, err := io.Copy(io.MultiWriter(tmp, h), r)
	if err != nil {
		return ir.NodeID{}, 0, fmt.Errorf("write blob: %w", err)
	}

	err = tmp.Close()
	if err != nil {
		return ir.NodeID{}, 0, fmt.Errorf("close blob: %w", err)
	}

	id := h.Sum()
	dst := s.path(id)

	err = os.MkdirAll(filepath.Dir(dst), 0o700)
	if err != nil {
		return ir.NodeID{}, 0, fmt.Errorf("create shard: %w", err)
	}

	// Already present: the bytes are identical by definition, so there is
	// nothing to do and nothing to overwrite.
	_, err = os.Stat(dst)
	if err == nil {
		return id, n, nil
	}

	err = os.Rename(tmp.Name(), dst)
	if err != nil {
		return ir.NodeID{}, 0, fmt.Errorf("commit blob: %w", err)
	}

	return id, n, nil
}

// Get returns the bytes named by id, having verified them.
//
// Verification is complete before any byte is returned. That is the expensive
// choice and the correct one: a streaming verifier can only detect corruption
// at the end, by which time a caller materialising a layer has already written
// bad files to disk. Correct-and-slower beats fast-and-occasionally-wrong here
// by an enormous margin.
//
// KNOWN GAP against green paper C.4, which requires that "a peer serving wrong
// bytes is detected within one chunk, not at the end of a transfer". That needs
// verified streaming - BLAKE3's BAO encoding - which neither Go implementation
// provides today. Until then, transfers are verified whole. Recorded rather
// than quietly ignored.
func (s *Store) Get(id ir.NodeID) ([]byte, error) {
	b, err := os.ReadFile(s.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}

		return nil, fmt.Errorf("read blob: %w", err)
	}

	h := ir.NewHasher()
	h.Fixed(b)

	if got := h.Sum(); got != id {
		return nil, fmt.Errorf("%w: stored as %s, hashes to %s", ErrCorrupt, id, got)
	}

	return b, nil
}

// Delete removes a blob. Garbage collection removes entries; it never rewrites
// them (I9), so this is the only way a blob leaves the store.
func (s *Store) Delete(id ir.NodeID) error {
	err := os.Remove(s.path(id))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete blob: %w", err)
	}

	return nil
}
