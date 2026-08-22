package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Index is a record of what the store holds, kept apart from the store.
//
// Redundant today, and deliberately so: the store is a directory anybody can
// stat, and this is the same answer arriving from the other side of a boundary
// that does not exist yet. It is built now because the risk it carries is not
// the lookup - it is *completeness*, whether every path that files a layer also
// records it, and that is a question worth answering while both answers are
// still available to compare (E542).
//
// **The invariant is one-sided: the index may lag the store, never lead it.**
//
//	a layer the store holds and the index does not  ->  a rebuild
//	a layer the index claims and the store lacks    ->  a cache hit against nothing
//
// The first costs time. The second is a wrong build that reports success, which
// is the one outcome this engine spends its invariants avoiding. Everything
// here follows from that asymmetry: note *after* filing, forget *before*
// deleting, and when in doubt say no.
//
// Located inside the store while the store is a directory. That is the one
// thing the disk changes: the point of an index is to be readable by a party
// that cannot read the store, so it moves to the host's own directory when the
// store stops being one.
type Index string

// path is where a layer's record lives.
func (i Index) path(id ir.NodeID) string {
	return filepath.Join(string(i), "index", id.String())
}

// Has reports whether the index records this layer.
//
// An unset index holds nothing, rather than answering from the working
// directory: `Index("")` would join to a relative path, and a wrong "no" is
// only a rebuild while a wrong "yes" is not.
func (i Index) Has(id ir.NodeID) bool {
	if i == "" {
		return false
	}

	_, err := os.Stat(i.path(id))

	return err == nil
}

// Note records that the store holds this layer.
//
// Called after the layer is filed and never before: an index entry that arrives
// first describes a layer that may never exist.
func (i Index) Note(id ir.NodeID) error {
	if i == "" {
		return nil
	}

	err := os.MkdirAll(filepath.Join(string(i), "index"), 0o750)
	if err != nil {
		return fmt.Errorf("prepare the store index: %w", err)
	}

	// An empty file: the name is the whole of the record. Created rather than
	// written, so a second noter costs an open and no bytes.
	f, err := os.OpenFile(i.path(id), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("record layer %s in the store index: %w", id, err)
	}

	return f.Close()
}

// Forget removes a layer's record.
//
// Called before the layer is deleted and never after, for the same reason Note
// is called after it is filed: between the two, the index must be the pessimistic
// one.
func (i Index) Forget(id ir.NodeID) error {
	if i == "" {
		return nil
	}

	err := os.Remove(i.path(id))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove layer %s from the store index: %w", id, err)
	}

	return nil
}

// Rebuild replaces the index with what the store actually holds.
//
// The index is derived, so losing it is recoverable and costs one walk: a host
// meeting an existing store for the first time, or one whose index was thrown
// away, asks the store directly and writes down the answer. It is the only
// operation here that reads the store to write the index, which is why it is
// the only one that will need a guest to perform it.
func (i Index) Rebuild() error {
	if i == "" {
		return nil
	}

	entries, err := os.ReadDir(filepath.Join(string(i), "layers"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // a store with no layers holds nothing to record
		}

		return fmt.Errorf("read the layer store to rebuild its index: %w", err)
	}

	// Built beside and renamed over, so an interrupted rebuild leaves the index
	// it had rather than half of a new one.
	staging, err := os.MkdirTemp(string(i), ".index-")
	if err != nil {
		return fmt.Errorf("stage a rebuilt store index: %w", err)
	}

	// Removed on every path but the successful one, where the rename has
	// already taken it away.
	done := false

	defer func() {
		if !done {
			_ = os.RemoveAll(staging)
		}
	}()

	var f *os.File

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		id, notALayer := ir.ParseNodeID(e.Name())
		if notALayer != nil {
			continue // staging directories and anything else that is not a layer
		}

		//nolint:gosec // a path this engine derived from a digest it filed
		f, err = os.OpenFile(filepath.Join(staging, id.String()), os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("record layer %s while rebuilding the store index: %w", id, err)
		}

		err = f.Close()
		if err != nil {
			return fmt.Errorf("record layer %s while rebuilding the store index: %w", id, err)
		}
	}

	at := filepath.Join(string(i), "index")

	err = os.RemoveAll(at)
	if err != nil {
		return fmt.Errorf("clear the old store index: %w", err)
	}

	err = os.Rename(staging, at)
	if err != nil {
		return fmt.Errorf("install the rebuilt store index: %w", err)
	}

	done = true

	return nil
}

// Disagrees reports where the index and the store differ, in both directions.
//
// Answerable only while the store is a directory this process can read, which
// is exactly why it exists now: it is the check that the index is complete,
// run while there is still something to check it against (E542). Once the store
// is a disk, only its owner can answer this, and by then the answer needs to
// have been "nowhere" for a long time.
//
// `missing` costs a rebuild of a layer the machine has. `claimed` is the
// serious one: a cache hit against a layer that is not there.
func (i Index) Disagrees() (missing, claimed []ir.NodeID, err error) {
	if i == "" {
		return nil, nil, nil
	}

	read := func(dir string) (map[ir.NodeID]bool, error) {
		out := map[ir.NodeID]bool{}

		entries, bad := os.ReadDir(filepath.Join(string(i), dir))
		if bad != nil {
			if errors.Is(bad, fs.ErrNotExist) {
				return out, nil
			}

			return nil, fmt.Errorf("read %s to compare it with the store index: %w", dir, bad)
		}

		for _, e := range entries {
			// Staging names are not layers, and neither is anything else this
			// engine did not put here under a digest.
			id, notALayer := ir.ParseNodeID(e.Name())
			if notALayer == nil {
				out[id] = true
			}
		}

		return out, nil
	}

	held, err := read("layers")
	if err != nil {
		return nil, nil, err
	}

	recorded, err := read("index")
	if err != nil {
		return nil, nil, err
	}

	for id := range held {
		if !recorded[id] {
			missing = append(missing, id)
		}
	}

	for id := range recorded {
		if !held[id] {
			claimed = append(claimed, id)
		}
	}

	return missing, claimed, nil
}
