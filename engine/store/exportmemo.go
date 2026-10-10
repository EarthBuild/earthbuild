package store

import (
	"os"
	"path/filepath"
	"strconv"
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

	// Guarded four lines above - empty, absolute and `..` are all refused - and
	// `rel` is a name this store wrote into its own memo. gosec traces the read
	// and not the refusal (G703).
	fi, err := os.Lstat(filepath.Join(root, rel)) //nolint:gosec // refused above
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

	m.write(exportMemoKey(stack, path), rel)
}

// write puts one answer in the memo, whole.
//
// **Written and renamed into place**, because a torn memo read by a concurrent
// build is an answer that names nothing - survivable, since every reader checks
// what it was told, but a rename costs nothing and keeps the failure impossible
// rather than merely harmless.
//
// Shared by both kinds of answer so there is one atomic write here rather than
// two that have to stay alike.
func (m ExportMemo) write(name, body string) {
	err := os.MkdirAll(m.dir, 0o750)
	if err != nil {
		return
	}

	at := filepath.Join(m.dir, name)

	f, err := os.CreateTemp(m.dir, ".note-*")
	if err != nil {
		return
	}

	tmp := f.Name()

	_, err = f.WriteString(body)
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

	err = os.Rename(tmp, at)
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

// outputMemoPrefix distinguishes an answer about this machine's copy from an
// answer about the store's.
//
// Two questions, one key derivation. `Lookup` answers "where in the store are
// these bytes", which only a backend sharing a filesystem can use; `Current`
// answers "does the destination already hold them", which every backend can.
const outputMemoPrefix = "out-"

// Current reports whether the destination already holds this artifact.
//
// **The export a build does not have to do.** `SAVE ARTIFACT AS LOCAL` of a
// 70 MiB binary costs 0.409s on a microVM - staged in the guest, fetched out
// over a block device, copied to the destination - and it is paid on every
// build, including one where all 94 steps hit the cache. The bytes are already
// on this machine when the last build put them there.
//
// **The key cannot go stale; the file can.** A stack is a list of
// content-addressed layers, so different bytes are a different key and cannot
// collide with this answer - which is why the check on the destination is only
// about the destination. Size and modification time, because somebody who edits
// or removes the exported file has to get it back and nothing in the build can
// know they did. A stat, not a digest: hashing 70 MiB to avoid copying 70 MiB
// is not a saving.
//
// Wrong in the safe direction by construction, as Lookup is: a memo that has
// been outlived says "not current" and the caller exports, which is what it
// would have done anyway.
func (m ExportMemo) Current(stack []ir.NodeID, path, dest string) bool {
	if m.dir == "" || dest == "" {
		return false
	}

	b, err := os.ReadFile(filepath.Join(m.dir, outputMemoPrefix+exportMemoKey(stack, path)))
	if err != nil {
		return false
	}

	want := strings.TrimSpace(string(b))
	if want == "" {
		return false
	}

	return want == outputStamp(dest)
}

// NoteOutput records that the destination now holds this artifact.
//
// Not an error worth returning, for the reason Note gives: the memo is an
// optimisation and a build that failed over its bookkeeping would be trading a
// correct answer for a tidy one.
func (m ExportMemo) NoteOutput(stack []ir.NodeID, path, dest string) {
	stamp := outputStamp(dest)
	if m.dir == "" || stamp == "" {
		return
	}

	m.write(outputMemoPrefix+exportMemoKey(stack, path), stamp)
}

// outputStamp identifies a file cheaply, or is empty where there is no file.
//
// Size and modification time to the nanosecond. Not an inode: a destination
// rewritten in place keeps one, and a rename onto it - which is how this engine
// and most editors write a file - changes it for a file whose contents did not,
// so it answers a different question in both directions.
func outputStamp(dest string) string {
	fi, err := os.Lstat(dest)
	if err != nil || !fi.Mode().IsRegular() {
		return ""
	}

	return strconv.FormatInt(fi.Size(), 10) + " " +
		strconv.FormatInt(fi.ModTime().UnixNano(), 10)
}
