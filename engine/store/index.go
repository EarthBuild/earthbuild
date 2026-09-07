package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Index is a record of what the store holds, kept apart from the store.
//
// Redundant today, and deliberately so: the store is a directory anybody can
// stat, and this is the same answer arriving from the other side of a boundary
// that does not exist yet. It is built now because the risk it carries is not
// the lookup - it is *completeness*, whether every path that files a layer also
// records it, and that is a question worth answering while both answers are
// still available to compare (E542, E543).
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
// A struct with an unexported field, rather than a string, so it cannot be
// conjured by conversion. Every index comes from OpenIndex, which is where an
// absent one is built - and *absent is not empty*: a store filled before this
// existed holds every layer it always did, and an index that answered "no"
// about all of them would throw away a cache nobody could see was gone. The
// zero value holds nothing, which is the safe direction and the only thing a
// caller can get without asking.
//
// Located inside the store while the store is a directory. That is the one
// thing the disk changes: the point of an index is to be readable by a party
// that cannot read the store, so it moves to the host's own directory when the
// store stops being one.
type Index struct{ dir string }

// OpenIndex returns a store's index, building it if it is not there.
//
// The build is the migration and the repair at once: a store that predates the
// index, or one whose index was thrown away, is walked once and written down.
// Losing the race to do that is success - another build wrote the same answer
// from the same store.
//
// An empty root yields the zero index, which holds nothing. `Index{}` joining to
// a relative path would answer from whatever directory the process happens to
// be in, and a stray `index/` there is not this store's.
func OpenIndex(root string) (Index, error) {
	if root == "" {
		return Index{}, nil
	}

	i := Index{dir: root}

	_, err := os.Stat(i.at())
	if err == nil {
		return i, nil
	}

	if !errors.Is(err, fs.ErrNotExist) {
		return Index{}, fmt.Errorf("open the store index at %s: %w", i.at(), err)
	}

	err = i.fill(false)
	if err != nil {
		return Index{}, err
	}

	return i, nil
}

// at is the index directory.
func (i Index) at() string { return filepath.Join(i.dir, "index") }

// path is where a layer's record lives.
func (i Index) path(id ir.NodeID) string { return filepath.Join(i.at(), id.String()) }

// Has reports whether the index records this layer.
func (i Index) Has(id ir.NodeID) bool {
	if i.dir == "" {
		return false
	}

	_, err := os.Stat(i.path(id))

	return err == nil
}

// Used is when this layer was last read, or the zero time if the index has never
// heard of it.
//
// The index entry's own timestamp, not the layer's. A layer's mtimes are part of
// what it *is* (I8), so a collector that dated layers by touching them would be
// editing the thing it is deciding about; the bookkeeping beside it carries no
// such meaning and is free to be written on.
func (i Index) Used(id ir.NodeID) time.Time {
	if i.dir == "" {
		return time.Time{}
	}

	fi, err := os.Stat(i.path(id))
	if err != nil {
		return time.Time{}
	}

	return fi.ModTime()
}

// Touch records that a layer was read just now.
//
// What turns "least recently written" into "least recently used", which is the
// difference between a collector that drops last month's throwaway layers and
// one that drops the base image every build starts from. Best-effort: a
// collection ordered by slightly stale times evicts a slightly wrong layer,
// which costs a rebuild, and failing a build over it would cost more.
func (i Index) Touch(id ir.NodeID) {
	if i.dir == "" {
		return
	}

	now := time.Now()

	_ = os.Chtimes(i.path(id), now, now)
}

// Note records that the store holds this layer.
//
// Called after the layer is filed and never before: an index entry that arrives
// first describes a layer that may never exist.
func (i Index) Note(id ir.NodeID) error {
	if i.dir == "" {
		return nil
	}

	err := os.MkdirAll(i.at(), 0o750)
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
// is called after it is filed: between the two, the index must be the
// pessimistic one.
func (i Index) Forget(id ir.NodeID) error {
	if i.dir == "" {
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
// The repair, asked for rather than stumbled into: OpenIndex fills an index that
// is *missing*, and this one replaces an index that is there and wrong. It is
// the only operation here that reads the store to write the index, which is why
// it is the only one that will need a guest to perform it.
func (i Index) Rebuild() error {
	if i.dir == "" {
		return nil
	}

	return i.fill(true)
}

// fill writes the index from the store, replacing an existing one only if told.
//
// Built beside and renamed over, so an interrupted fill leaves the index it had
// rather than half of a new one - and so a fill that is only filling a gap can
// lose to another process without either of them seeing a partial index.
func (i Index) fill(replace bool) error {
	entries, err := os.ReadDir(filepath.Join(i.dir, "layers"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // a store with no layers holds nothing to record
		}

		return fmt.Errorf("read the layer store to build its index: %w", err)
	}

	staging, err := os.MkdirTemp(i.dir, ".index-")
	if err != nil {
		return fmt.Errorf("stage a store index: %w", err)
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
			return fmt.Errorf("record layer %s while building the store index: %w", id, err)
		}

		err = f.Close()
		if err != nil {
			return fmt.Errorf("record layer %s while building the store index: %w", id, err)
		}
	}

	if replace {
		err = os.RemoveAll(i.at())
		if err != nil {
			return fmt.Errorf("clear the old store index: %w", err)
		}
	}

	err = os.Rename(staging, i.at())
	if err != nil {
		// Somebody else filled the gap while this was walking, which is the
		// same answer read from the same store. Only when filling a gap: a
		// replace that finds one is a replace that did not happen.
		if replace || !indexPresent(i.at()) {
			return fmt.Errorf("install the store index: %w", err)
		}

		return nil
	}

	done = true

	return nil
}

// indexPresent reports whether an index directory is there.
func indexPresent(at string) bool {
	fi, err := os.Stat(at)

	return err == nil && fi.IsDir()
}

// Disagrees reports where the index and the store differ, in both directions.
//
// Answerable only while the store is a directory this process can read, which
// is exactly why it exists now: it is the check that the index is complete, run
// while there is still something to check it against (E542). Once the store is
// a disk, only its owner can answer this, and by then the answer needs to have
// been "nowhere" for a long time.
//
// `missing` costs a rebuild of a layer the machine has. `claimed` is the serious
// one: a cache hit against a layer that is not there.
func (i Index) Disagrees() (missing, claimed []ir.NodeID, err error) {
	if i.dir == "" {
		return nil, nil, nil
	}

	read := func(dir string) (map[ir.NodeID]bool, error) {
		out := map[ir.NodeID]bool{}

		entries, bad := os.ReadDir(filepath.Join(i.dir, dir))
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
