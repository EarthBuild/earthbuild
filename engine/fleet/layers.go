package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// Layers is a directory of layers, as both a source and a destination.
//
// The thing E261 found missing. A step's inputs are **layers**, and a layer on
// disk is a directory; the transfer protocol moves bytes. This is the conversion:
// `Get` packs a layer on demand and `Put` unpacks one, so a layer moves over the
// same protocol as anything else and the two sides never have to agree on a
// second name for it.
//
// The path layout is `LayerStore`'s, because it is the same store: a layer
// fetched here has to be the layer a materialise finds.
type Layers struct {
	// Root is the store directory - the one holding `layers/`.
	Root string

	// proofs are the manifests of layers this store has been asked about. See
	// Manifest.
	proofMu sync.Mutex
	proofs  map[ir.NodeID][]byte
}

// Has reports whether this store holds the layer.
func (l *Layers) Has(id ir.NodeID) bool {
	fi, err := os.Stat(l.at(id))

	return err == nil && fi.IsDir()
}

// Get packs a layer for sending.
//
// Packed on demand rather than kept packed. A store that held both forms would
// have to keep them in step, and the pack is a deterministic function of the
// tree - so the only thing storing it buys is a cache, at the price of a second
// thing that can be stale.
func (l *Layers) Get(id ir.NodeID) ([]byte, error) {
	if !l.Has(id) {
		return nil, fmt.Errorf("no layer %v here", id)
	}

	var buf pipeBuffer

	err := layer.PackOwned(l.at(id), &buf, nil, l.owners(id))
	if err != nil {
		return nil, fmt.Errorf("pack layer %v: %w", id, err)
	}

	return buf.b, nil
}

// ownersAt is where a layer's declared ownership is kept.
//
// Beside the tree rather than inside it: anything inside would be a file the
// layer does not have and the digest would name it.
func (l *Layers) ownersAt(id ir.NodeID) string { return l.at(id) + ".own" }

// owners is what this store was told a layer's files are owned by.
//
// Absent for a layer this machine made itself, where the disk is the authority
// and no declaration is needed. Absent *also* when the sidecar cannot be read,
// which is the same answer as "there is none" and is right: the fallback is the
// disk, and a layer whose ownership then does not reproduce is caught by the
// digest check at the far end rather than served as a silent substitution.
func (l *Layers) owners(id ir.NodeID) map[string]layer.Owner {
	b, err := os.ReadFile(l.ownersAt(id))
	if err != nil {
		return nil
	}

	var out map[string]layer.Owner

	if json.Unmarshal(b, &out) != nil {
		return nil
	}

	return out
}

// keepOwners records a declaration, if there is anything in it to record.
//
// **Written before the rename, removed with the layer's temporary directory if
// the rename never happens.** A sidecar for a layer that is not there would be
// read by a later Put of the same digest, which is the one case where a stale
// file would be believed.
func (l *Layers) keepOwners(id ir.NodeID, own map[string]layer.Owner) error {
	if len(own) == 0 {
		return nil
	}

	b, err := json.Marshal(own)
	if err != nil {
		return fmt.Errorf("record who owns layer %v: %w", id, err)
	}

	return os.WriteFile(l.ownersAt(id), b, 0o600)
}

// Put unpacks a layer and files it under the digest it actually has.
//
// **The caller does not get to choose the name.** A layer is a directory named
// by its digest, so a store that filed what arrived under the digest that was
// asked for would serve corruption for ever after, and every key derived from
// that base would name something else (§5.3). What arrives is unpacked beside
// the store, captured, and only then renamed into place under its own name.
//
// A transfer that fails leaves nothing: a half-unpacked directory sitting under
// the right digest is worse than no layer at all, because `Has` would say yes
// and the build would proceed on a tree missing files.
func (l *Layers) Put(r io.Reader) (ir.NodeID, int64, error) {
	err := os.MkdirAll(filepath.Join(l.Root, "layers"), 0o755)
	if err != nil {
		return ir.NodeID{}, 0, fmt.Errorf("make the layer store: %w", err)
	}

	// Beside the store rather than in /tmp: a rename across filesystems is a
	// copy, and a layer is the largest thing this engine moves.
	tmp, err := os.MkdirTemp(filepath.Join(l.Root, "layers"), ".incoming-")
	if err != nil {
		return ir.NodeID{}, 0, fmt.Errorf("make room for an incoming layer: %w", err)
	}

	// Removed on every path but the successful one, where the rename has already
	// taken it away.
	done := false

	defer func() {
		if !done {
			_ = os.RemoveAll(tmp)
		}
	}()

	// **What the stream declares, not what the filesystem accepted.**
	//
	// A layer's identity includes uid and gid (§3.3), and restoring ownership
	// needs privilege a worker does not have - so every file lands owned by
	// whoever ran the worker. Capturing that names a layer nobody sent, and the
	// worker reports that the peer did not hold what it had just received
	// (E313). Two machines with different users could not share a base at all.
	//
	// Sound because the declaration is checked, not trusted: `Provision` insists
	// the digest that comes back is the one it asked for, so a peer that lies
	// about ownership produces a layer that is rejected rather than filed
	// (§5.3). What this removes is the receiver's *own* user leaking into an
	// identity that is supposed to be the sender's.
	own, err := layer.UnpackOwned(r, tmp)
	if err != nil {
		return ir.NodeID{}, 0, fmt.Errorf("unpack an incoming layer: %w", err)
	}

	c, err := layer.TakeOwnedIn(tmp, layer.IDMap{}, layer.IDMap{}, own)
	if err != nil {
		return ir.NodeID{}, 0, fmt.Errorf("capture an incoming layer: %w", err)
	}

	at := l.at(c.ID)

	// Already here - two workers can send the same base at once. The copy that
	// arrived second is discarded rather than renamed over the first, because a
	// rename onto a directory fails and because whichever is already there has
	// been checked exactly as hard.
	if l.Has(c.ID) {
		return c.ID, c.Bytes, nil
	}

	err = os.Rename(tmp, at)
	if err != nil {
		// **Somebody else filed it between the check above and here.**
		//
		// The check is not a lock and cannot be one: two steps fetching the
		// same input at once both unpack, both find the layer absent, and the
		// loser's rename lands on a directory that now exists. A step that got
		// its input perfectly well then reported that it could not (E347).
		//
		// *Failure class: TOCTOU on a check-then-act.* The remedy is not a lock
		// either - the winner's copy was verified exactly as hard as this one,
		// because a layer is named by what it contains (§5.3), so the answer to
		// losing is that the layer is there.
		if l.Has(c.ID) {
			return c.ID, c.Bytes, nil
		}

		return ir.NodeID{}, 0, fmt.Errorf("file layer %v: %w", c.ID, err)
	}

	// After the rename: the layer is the thing, and a declaration beside a
	// layer that does not exist would outlive a failed transfer.
	err = l.keepOwners(c.ID, own)
	if err != nil {
		return ir.NodeID{}, 0, err
	}

	done = true

	return c.ID, c.Bytes, nil
}

// Size is how many bytes of file content a layer holds.
//
// Walked rather than recorded: a layer is a directory and its size is a property
// of what is in it, so a stored number would be a second thing to keep in step
// with the first. The caller memoises - `Size` is asked once per layer per
// build, not once per step (E330).
//
// Content only, matching `Capture.Bytes`: what the scheduler is pricing is what
// would cross a network, and directory entries do not.
func (l *Layers) Size(id ir.NodeID) (int64, bool) {
	if !l.Has(id) {
		return 0, false
	}

	var total int64

	err := filepath.WalkDir(l.at(id), func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		fi, err := d.Info()
		if err != nil {
			// A file that vanished mid-walk. Not fatal: this is a hint, and a
			// hint that is a little small is better than a build that stops.
			return nil //nolint:nilerr // a size is advice
		}

		if fi.Mode().IsRegular() {
			total += fi.Size()
		}

		return nil
	})
	if err != nil {
		return 0, false
	}

	return total, true
}

// RootDir is where this store keeps its layers, and anything kept beside them.
func (l *Layers) RootDir() string { return l.Root }

func (l *Layers) at(id ir.NodeID) string {
	return filepath.Join(l.Root, "layers", id.String())
}

// pipeBuffer is a growable sink for a pack.
//
// `bytes.Buffer` would do; this exists so the one place that holds a whole layer
// in memory is named and easy to find when it stops being acceptable. A layer of
// a gigabyte is a gigabyte here, which is the cost of `Source` handing back
// readers over buffers (see Fetch) and is written down there too.
type pipeBuffer struct{ b []byte }

func (p *pipeBuffer) Write(b []byte) (int, error) {
	p.b = append(p.b, b...)

	return len(b), nil
}

// ErrNotALayer marks bytes that did not unpack into the layer they claimed.
var ErrNotALayer = errors.New("not the layer that was asked for")

// Store is a place layers are kept: filled by fetching, read by serving.
//
// Both halves, because a driver is both - it fills its store bringing a worker's
// result back (E274) and reads it serving the base of the build (E277).
type Store interface {
	Keeper
	Held
}

var _ Store = (*Layers)(nil)

// LayerSource serves packed layers.
//
// Distinct from `StoreSource` because the two carry different things and check
// them differently. A blob is named by the digest of its bytes, so it travels as
// a bao encoding and a liar is caught within a chunk. A **layer** is named by
// the digest of its tree, and the bytes that carry it have no relation to that
// name - so it travels as a plain pack and is identified by unpacking it (E263).
//
// The cost is written down rather than hidden: on this path a peer serving a
// gigabyte of rubbish is detected after the gigabyte, not after a chunk. What
// would restore the early hang-up is sending the pack's own root alongside it,
// so the transfer can be checked for corruption as it arrives while identity
// still comes from the capture. That is worth doing and is not free.
type LayerSource struct {
	// Label names this source in diagnostics.
	Label string
	// Held is where the layers are.
	Held Held
}

// Name is this source's label.
func (s *LayerSource) Name() string {
	if s.Label == "" {
		return "layers"
	}

	return s.Label
}

// Fetch packs what this store has of these layers.
//
// Absences are silent, because a source not having a layer is what the next
// source is for.
func (s *LayerSource) Fetch(
	_ context.Context, ids []ir.NodeID,
) (map[ir.NodeID]io.Reader, error) {
	out := make(map[ir.NodeID]io.Reader, len(ids))

	for _, id := range ids {
		if s.Held == nil || !s.Held.Has(id) {
			continue
		}

		b, err := s.Held.Get(id)
		if err != nil {
			continue
		}

		out[id] = bytes.NewReader(b)
	}

	return out, nil
}
