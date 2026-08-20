package fleet

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// Fragments holds parts of layers, where they cannot be mistaken for layers.
//
// **A fragment is not the layer** (E281). It is named by *both* halves of what
// it is - which layer, and which paths - and it lives under `fragments/` where
// `LayerStore.Has` does not look. That separation is structural rather than a
// discipline, because the failure it prevents is a build that succeeds while
// standing on part of a base (E282).
//
// Both halves of the name are load-bearing. Two bases commonly share a path -
// `/etc/hosts` is in every image - so a fragment named only by its paths would
// serve one image's file as another's. And a fragment named only by its layer
// would answer for paths it does not contain.
type Fragments struct {
	// Root is the store directory, shared with Layers - the same store, a
	// different shelf.
	Root string
}

// manifestAt is where a layer's proof is kept, once, beside its fragments.
func (f *Fragments) manifestAt(id ir.NodeID) string {
	return filepath.Join(f.Root, "fragments", id.String(), "manifest")
}

// HasManifest reports whether this worker already holds a layer's proof.
//
// **A manifest crosses once per layer, not once per fragment.** It is about a
// hundred bytes an entry, and a fragment is the bytes actually read - so for the
// case lazy transfer exists for, a small read set from a large base, the manifest
// is the dominant cost: five thousand files and ten paths read moved 534 KB of
// proof against 83 KB of content (E298, E299).
func (f *Fragments) HasManifest(id ir.NodeID) bool {
	fi, err := os.Stat(f.manifestAt(id))

	return err == nil && !fi.IsDir()
}

// Manifest is a layer's proof, if this worker has it.
func (f *Fragments) Manifest(id ir.NodeID) ([]byte, bool) {
	b, err := os.ReadFile(f.manifestAt(id)) //nolint:gosec // a path this engine composed
	if err != nil {
		return nil, false
	}

	return b, true
}

// keepManifest stores a proof that has already been checked against the layer's
// name.
//
// Only a verified one, and that is the whole of why this is a separate step from
// receiving it: an unverified manifest kept here would be a forgery every later
// fragment of that layer is checked against - checked once, trusted for ever.
func (f *Fragments) keepManifest(id ir.NodeID, manifest []byte) {
	at := f.manifestAt(id)

	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		return
	}

	tmp := at + ".incoming"

	if err := os.WriteFile(tmp, manifest, 0o600); err != nil {
		return
	}

	if err := os.Rename(tmp, at); err != nil {
		_ = os.Remove(tmp)
	}
}

// Has reports whether this exact fragment is here.
func (f *Fragments) Has(id ir.NodeID, want []string) bool {
	fi, err := os.Stat(f.Dir(id, want))

	return err == nil && fi.IsDir()
}

// Dir is where a fragment of these paths of this layer lives.
//
// The name is a function of what the fragment *contains*, so one set of paths
// listed in two orders is one fragment. Otherwise two predictions of the same
// read set would fetch it twice and keep both, which is the cache growing rather
// than being used.
func (f *Fragments) Dir(id ir.NodeID, want []string) string {
	return filepath.Join(f.Root, "fragments", id.String(), nameOf(want))
}

// nameOf is a digest of a path set, order and repeats removed.
func nameOf(want []string) string {
	clean := make([]string, 0, len(want))

	for _, p := range want {
		p = strings.TrimPrefix(filepath.Clean(p), "/")
		if p != "" && p != "." {
			clean = append(clean, p)
		}
	}

	slices.Sort(clean)
	clean = slices.Compact(clean)

	h := ir.NewHasher()
	h.Count(len(clean))

	for _, p := range clean {
		h.Str(p)
	}

	return h.Sum().String()
}

// PutVerified keeps a fragment only if its manifest says it belongs.
//
// Two checks, in this order and for a reason:
//
//  1. **the manifest hashes to the layer's name.** That is what makes it a proof
//     rather than a description: a peer sending a manifest of its own devising
//     could otherwise authenticate anything it liked (E285). Checked first
//     because it is one hash of a couple of megabytes, against unpacking a
//     fragment and walking it;
//  2. every file in the fragment matches the digest the manifest gives for that
//     path, and no path is present that the manifest does not mention.
//
// A fragment that fails either leaves nothing behind, as one that arrives
// truncated does.
func (f *Fragments) PutVerified(
	id ir.NodeID, want []string, manifest []byte, r io.Reader,
) error {
	if got := layer.ManifestID(manifest); got != id {
		return fmt.Errorf("%w: a manifest for %v was offered as %v",
			layer.ErrMalformed, got, id)
	}

	at := f.Dir(id, want)

	tmp, err := f.unpackBeside(at, r)
	if err != nil {
		return err
	}

	done := false

	defer func() {
		if !done {
			_ = os.RemoveAll(tmp)
		}
	}()

	err = layer.VerifyFragment(manifest, tmp)
	if err != nil {
		return fmt.Errorf("a fragment of %v: %w", id, err)
	}

	// Verified, so it is worth keeping: every later fragment of this layer can
	// be checked against it without it crossing again (E299).
	f.keepManifest(id, manifest)

	if f.Has(id, want) {
		return nil
	}

	err = os.Rename(tmp, at)
	if err != nil {
		return fmt.Errorf("file a fragment of %v: %w", id, err)
	}

	done = true

	return nil
}

// unpackBeside unpacks a fragment next to where it will live, so that filing it
// is a rename rather than a copy (E263).
func (f *Fragments) unpackBeside(at string, r io.Reader) (string, error) {
	err := os.MkdirAll(filepath.Dir(at), 0o755)
	if err != nil {
		return "", fmt.Errorf("make room for a fragment: %w", err)
	}

	tmp, err := os.MkdirTemp(filepath.Dir(at), ".incoming-")
	if err != nil {
		return "", fmt.Errorf("make room for a fragment: %w", err)
	}

	// **No ownership declaration is kept, unlike a whole layer.**
	//
	// A whole layer needs one because its identity includes ownership and an
	// unprivileged unpack cannot restore it (E313). A **fragment** is judged by
	// a seal that deliberately excludes ownership, for exactly that reason
	// (E324) - so a relay packing from its own disk sends something the next
	// machine accepts, and a declaration here would change nothing anybody can
	// observe.
	//
	// It was written, and then deleted when mutation testing could not kill it:
	// an unobservable safeguard is the failure this project keeps meeting from
	// the other side.
	err = layer.Unpack(r, tmp)
	if err != nil {
		_ = os.RemoveAll(tmp)

		return "", fmt.Errorf("unpack a fragment: %w", err)
	}

	return tmp, nil
}

// Fragment is the part of a layer somebody asked for, and the proof it belongs.
//
// Both together, because neither is any use alone: a fragment without a manifest
// cannot be checked (E282), and a manifest without a fragment authenticates
// nothing anybody has.
func (l *Layers) Fragment(id ir.NodeID, want []string) (manifest, packed []byte, err error) {
	if !l.Has(id) {
		return nil, nil, fmt.Errorf("no layer %v here", id)
	}

	at := l.at(id)

	manifest, err = l.Manifest(id)
	if err != nil {
		return nil, nil, err
	}

	var buf pipeBuffer

	err = layer.PackOwned(at, &buf, want, l.owners(id))
	if err != nil {
		return nil, nil, fmt.Errorf("pack a fragment of %v: %w", id, err)
	}

	return manifest, buf.b, nil
}

// Fragment serves on the part of a layer this worker holds.
//
// **Without it lazy transfer is a star.** Fragments come from whoever has the
// whole layer, so a worker that has just fetched exactly the bytes the next
// machine needs cannot pass them on, and adding machines adds queueing at the
// driver rather than throughput - E260 again, on the path that since E323 is
// the one that wins.
//
// Only the **same** set of paths, because a fragment is stored under a name
// derived from what it contains: a worker holding {a, b} could in principle
// serve {a}, and re-packing a subset of a subset is a second way of deciding
// what a fragment is. One is enough until a measurement asks for the other.
//
// The ownership is the origin's, not this machine's reading of the disk, and the
// proof is the one that arrived - a relay re-deriving either would be answering
// about a layer only it can see.
func (f *Fragments) Fragment(
	_ context.Context, id ir.NodeID, want []string, proof bool,
) (manifest, packed []byte, err error) {
	if !f.Has(id, want) {
		// Refused rather than answered with what is here. An empty fragment
		// verifies against any manifest - it contains nothing that contradicts
		// it - so a relay guessing would send a reply the asker accepts and
		// then faults on every file it expected (E325).
		return nil, nil, fmt.Errorf("%w: no fragment of %v here", ErrNotFetched, id)
	}

	if proof {
		m, ok := f.Manifest(id)
		if !ok {
			return nil, nil, fmt.Errorf("%w: no proof of %v here", ErrNotFetched, id)
		}

		manifest = m
	}

	var buf pipeBuffer

	err = layer.Pack(f.Dir(id, want), &buf)
	if err != nil {
		return nil, nil, fmt.Errorf("pack a fragment of %v: %w", id, err)
	}

	return manifest, buf.b, nil
}

// Manifest is a layer's proof, computed once.
//
// **A pure function of a stored layer**: the tree does not change under a
// digest, so every fragment after the first can have it for nothing. Serving one
// walked the layer twice and hashed every file's contents both times, which is
// most of what a fragment cost - 26ms for a 200-file layer, on loopback, to send
// one file (E337).
//
// Memoised in memory rather than beside the layer. It is derivable, so a stored
// copy would be a second thing to keep in step with the tree, and the case it
// exists for is one build asking many times.
func (l *Layers) Manifest(id ir.NodeID) ([]byte, error) {
	l.proofMu.Lock()
	defer l.proofMu.Unlock()

	if m, ok := l.proofs[id]; ok {
		return m, nil
	}

	m, err := layer.ManifestOwned(l.at(id), layer.IDMap{}, layer.IDMap{}, l.owners(id))
	if err != nil {
		return nil, fmt.Errorf("take a manifest of %v: %w", id, err)
	}

	if l.proofs == nil {
		l.proofs = map[ir.NodeID][]byte{}
	}

	l.proofs[id] = m

	return m, nil
}
