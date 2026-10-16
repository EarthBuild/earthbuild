package guest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// syncDigests is what the store already wrote down about two files, so `--sync`
// can tell them apart without reading either.
//
// **Both sides, or neither.** A digest is only an answer when it is available
// for the source *and* the destination: one known digest against one unknown
// file says nothing, so the copy falls back to the byte comparison it has always
// done. Every miss is a slow correct answer, which is why every lookup here may
// safely refuse.
type syncDigests struct {
	// src answers for a path inside the layer store: `<layerDir>/layers/<id>/…`.
	src func(abs string, size int64) (ir.NodeID, bool)
	// dst answers for a path in the step's merged view, from the manifests of
	// the layers below it.
	dst func(abs string, size int64) (ir.NodeID, bool)
}

// same reports whether two files hold the same bytes, and whether it knows.
func (d syncDigests) same(src string, srcSize int64, dst string, dstSize int64) (bool, bool) {
	if d.src == nil || d.dst == nil {
		return false, false
	}

	a, ok := d.src(src, srcSize)
	if !ok {
		return false, false
	}

	b, ok := d.dst(dst, dstSize)
	if !ok {
		return false, false
	}

	return a == b, true
}

// syncDigests assembles the oracle for one copy into one handle's filesystem.
//
// Per copy, so the manifests it reads are read once for the whole tree and then
// dropped. A layer's manifest never changes - the layer is named by it - so the
// only reason not to keep them for the life of the process is that nothing has
// yet asked for that.
func (s *Server) syncDigests(handle string, h core.Handle) syncDigests {
	if s.LayerDir == "" {
		return syncDigests{}
	}

	s.mu.Lock()
	base := s.bases[handle]
	s.mu.Unlock()

	kept := &manifests{layerDir: s.LayerDir, byLayer: map[string]map[string]layer.File{}}

	return syncDigests{
		src: kept.inTheStore,
		dst: kept.below(h.Root(), h.Delta(), base),
	}
}

// manifests reads a layer's manifest once and remembers what it said.
type manifests struct {
	layerDir string
	byLayer  map[string]map[string]layer.File
}

// files is the manifest for one layer, or nil where there is none.
//
// Nil is cached too: a layer stored before manifests were kept has none, and
// asking the filesystem again for every file of a tree would turn a missing
// optimisation into a slower copy than the one it replaced.
func (m *manifests) files(id string) map[string]layer.File {
	kept, ok := m.byLayer[id]
	if ok {
		return kept
	}

	m.byLayer[id] = nil

	parsed, err := ir.ParseNodeID(id)
	if err != nil {
		return nil
	}

	raw, ok, err := store.ReadManifest(m.layerDir, parsed)
	if err != nil || !ok {
		return nil
	}

	files, err := layer.Files(raw)
	if err != nil {
		return nil
	}

	m.byLayer[id] = files

	return files
}

// lookup is one path in one layer's manifest, refused unless the manifest and
// the file on disk agree about the size.
//
// **The size is the check that the manifest is about this file.** A digest is a
// claim about bytes the reader is not going to look at, so the one field it can
// confirm for free is the one worth confirming: a layer directory that does not
// match its manifest is refused rather than believed.
func (m *manifests) lookup(id, rel string, size int64) (ir.NodeID, bool) {
	f, ok := m.files(id)[rel]
	if !ok || f.Size != size {
		return ir.NodeID{}, false
	}

	return f.Content, true
}

// inTheStore answers for a path the copy is reading out of the layer store.
//
// The layer is read off the path rather than taken from the copy's own stack:
// every path under `<layerDir>/layers/<id>` is that layer by construction, and a
// symlink followed across the stack lands in a layer the caller never named.
func (m *manifests) inTheStore(abs string, size int64) (ir.NodeID, bool) {
	prefix := filepath.Join(m.layerDir, "layers") + string(filepath.Separator)

	rest, found := strings.CutPrefix(filepath.Clean(abs), prefix)
	if !found {
		return ir.NodeID{}, false
	}

	id, rel, found := strings.Cut(rest, string(filepath.Separator))
	if !found || rel == "" {
		return ir.NodeID{}, false
	}

	return m.lookup(id, filepath.ToSlash(rel), size)
}

// below answers for a path in the merged view, from the layers under it.
//
// Two conditions, and the first is the one that makes this safe:
//
//   - **a path in the step's own delta is not the base's any more.** The
//     manifest describes what the layer held; the step may have rewritten it,
//     and the merged view shows the rewrite. One `lstat` of the upper directory
//     settles it, which is what `ownWrites` does for observations;
//   - otherwise the newest layer naming the path decides, exactly as the mount
//     does. A whiteout or a directory in that layer is not a regular file, so it
//     has no entry in `Files` and the answer is refused.
func (m *manifests) below(root, delta string, base []ir.NodeID) func(string, int64) (ir.NodeID, bool) {
	if root == "" || delta == "" || len(base) == 0 {
		return nil
	}

	return func(abs string, size int64) (ir.NodeID, bool) {
		rel, err := filepath.Rel(root, abs)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			return ir.NodeID{}, false
		}

		_, err = os.Lstat(filepath.Join(delta, rel))
		if err == nil {
			return ir.NodeID{}, false
		}

		// Newest first: the stack arrives oldest-first (green paper §3.2), and
		// the topmost layer holding a path is the one the merged view reads.
		for _, above := range slices.Backward(base) {
			id, name := above.String(), filepath.ToSlash(rel)

			if _, has := m.files(id)[name]; !has {
				continue
			}

			return m.lookup(id, name, size)
		}

		return ir.NodeID{}, false
	}
}
