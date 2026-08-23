package store

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// exportMemoDir is where the answers live, inside the store.
const exportMemoDir = "exportmemo"

// ExportMemo remembers where an exported artifact sits in the store.
//
// Resolving an artifact means mounting its stack and asking overlayfs which
// layer wins - and on a fully cached build that mount is the only reason the
// engine wakes its sandbox at all (E568, E569). The answer is worth keeping
// because it cannot change: a stack is a list of content-addressed layers and a
// path is a path, so the same pair names the same bytes forever.
//
// **Unlike Index, a wrong answer here is not a wrong build.** The memo names a
// file; Lookup stats it and refuses when it is not there. So the failure mode of
// a memo that leads the store - the one Index spends its invariant avoiding - is
// a miss and a mount, which is exactly what would have happened anyway. That is
// why this may be written cheerfully and read without ceremony.
//
// A struct with an unexported field, for the reason Index has one: every memo
// comes from OpenExportMemo, and an empty root must not resolve against whatever
// directory the process is sitting in.
type ExportMemo struct{ dir string }

// OpenExportMemo returns a store's export memo.
//
// An empty root yields the zero memo, which remembers nothing and is asked
// nothing - the honest answer for a caller with no store.
func OpenExportMemo(root string) ExportMemo {
	if root == "" {
		return ExportMemo{}
	}

	return ExportMemo{dir: filepath.Join(root, exportMemoDir)}
}

// Lookup returns where the artifact sits in the store, relative to its root.
//
// The second result is false whenever the answer cannot be used, which covers
// having no memo, having no entry, and having an entry whose file has since been
// collected. The caller mounts, which is what it would have done regardless.
func (m ExportMemo) Lookup(stack []ir.NodeID, path string) (string, bool) {
	if m.dir == "" {
		return "", false
	}

	b, err := os.ReadFile(filepath.Join(m.dir, exportMemoKey(stack, path)))
	if err != nil {
		return "", false
	}

	rel := strings.TrimSpace(string(b))
	if rel == "" || filepath.IsAbs(rel) || strings.Contains(rel, "..") {
		return "", false
	}

	// The stat is what makes the memo safe rather than merely fast: it is the
	// difference between "the store still holds this" and "the store held this
	// when somebody last looked".
	root := filepath.Dir(m.dir)

	fi, err := os.Lstat(filepath.Join(root, rel))
	if err != nil || !fi.Mode().IsRegular() {
		return "", false
	}

	return rel, true
}

// Note records where an artifact was found.
//
// Failing to write is not an error worth returning. The memo is an optimisation
// whose absence costs a mount, and a build that fails because it could not write
// down something it did not need would be trading a correct answer for a
// bookkeeping one.
func (m ExportMemo) Note(stack []ir.NodeID, path, rel string) {
	if m.dir == "" || rel == "" {
		return
	}

	err := os.MkdirAll(m.dir, 0o750)
	if err != nil {
		return
	}

	// Written whole and renamed into place, because a torn memo read by a
	// concurrent build is a path that names nothing - survivable, since Lookup
	// stats it, but a rename costs nothing and keeps the failure impossible
	// rather than merely harmless.
	name := filepath.Join(m.dir, exportMemoKey(stack, path))

	f, err := os.CreateTemp(m.dir, ".note-*")
	if err != nil {
		return
	}

	tmp := f.Name()

	_, err = f.WriteString(rel)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)

		return
	}

	err = f.Close()
	if err != nil {
		_ = os.Remove(tmp)

		return
	}

	err = os.Rename(tmp, name)
	if err != nil {
		_ = os.Remove(tmp)
	}
}

// exportMemoKey is ℋ over the stack and the path.
//
// The engine's own encoding rather than a joined string: a stack is a sequence
// and a path is text, and "layer-a/layer-b" plus "c" must not collide with
// "layer-a" plus "layer-b/c". That is the injectivity green paper §1.4 requires,
// and here its absence would export one artifact in place of another.
func exportMemoKey(stack []ir.NodeID, path string) string {
	h := ir.NewHasher()

	h.Count(len(stack))

	for _, id := range stack {
		h.Fixed(id[:])
	}

	h.Str(path)

	return h.Sum().String()
}
