package guest

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A destination whose bytes already match is left exactly as it is.
//
// **The mtime is the point.** cargo decides what to recompile by comparing a
// source's mtime against the artefact built from it, so a copy that rewrites an
// unchanged file makes every file in the tree look newer than everything built
// from it - and the whole tree recompiles. Leaving it alone is what lets a
// published build tree be stood on.
func TestCopyingAnIdenticalFileLeavesItAlone(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "src.rs")
	dst := filepath.Join(dir, "dst.rs")

	same := []byte("fn main() {}\n")
	writeAt(t, src, same)
	writeAt(t, dst, same)

	// The destination is older, as an artefact's source in a restored tree is.
	old := time.Unix(1_700_000_000, 0)
	touchAt(t, dst, old)

	err := copyFileUnlessSame(src, dst, 0o644, true)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}

	if got := statOf(t, dst).ModTime(); !got.Equal(old) {
		t.Errorf("an identical file was rewritten: mtime moved to %v", got)
	}
}

// A destination that differs is written, and is then unambiguously newer.
func TestCopyingADifferentFileWritesIt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "src.rs")
	dst := filepath.Join(dir, "dst.rs")

	writeAt(t, src, []byte("fn main() { changed() }\n"))
	writeAt(t, dst, []byte("fn main() {}\n"))

	old := time.Unix(1_700_000_000, 0)
	touchAt(t, dst, old)

	err := copyFileUnlessSame(src, dst, 0o644, true)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}

	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}

	if string(body) != "fn main() { changed() }\n" {
		t.Errorf("the destination still reads %q", body)
	}

	if statOf(t, dst).ModTime().Equal(old) {
		t.Error("a changed file kept its old mtime, so nothing downstream will" +
			" notice it changed")
	}
}

// Same size, different bytes: the case a length check alone would wave through,
// which would be a wrong build rather than a slow one.
func TestASameSizedDifferenceIsNotSkipped(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "a")
	dst := filepath.Join(dir, "b")

	writeAt(t, src, []byte("aaaaBaaaa"))
	writeAt(t, dst, []byte("aaaaAaaaa"))

	err := copyFileUnlessSame(src, dst, 0o644, true)
	if err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}

	if string(body) != "aaaaBaaaa" {
		t.Errorf("a same-sized difference was skipped: %q", body)
	}
}

// Off, it writes whatever it is given - which is what every COPY did before the
// flag existed, and what one without the flag must still do.
func TestWithoutTheFlagAnIdenticalFileIsStillRewritten(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	same := []byte("identical")
	writeAt(t, src, same)
	writeAt(t, dst, same)

	old := time.Unix(1_700_000_000, 0)
	touchAt(t, dst, old)

	err := copyFileUnlessSame(src, dst, 0o644, false)
	if err != nil {
		t.Fatal(err)
	}

	if statOf(t, dst).ModTime().Equal(old) {
		t.Error("the copy was skipped without the flag asking for it")
	}
}

// A destination that is not there is written, flag or no flag.
func TestAnAbsentDestinationIsWritten(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "src")

	writeAt(t, src, []byte("new"))

	dst := filepath.Join(dir, "dst")

	err := copyFileUnlessSame(src, dst, 0o644, true)
	if err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("nothing was written: %v", err)
	}

	if string(body) != "new" {
		t.Errorf("wrote %q", body)
	}
}

// What the source no longer has, the destination no longer has.
//
// **This is the half the name promises.** `COPY` merges, everywhere and always,
// which is right for an ordinary base and wrong for one that already holds a
// previous copy of the same tree: a file you delete survives, and a build that
// reads the directory rather than a manifest goes on compiling it. Measured
// before this existed - the deleted file was still there after the copy.
func TestSyncRemovesWhatTheSourceNoLongerHas(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")

	mkdirAt(t, filepath.Join(src, "sub"))
	mkdirAt(t, filepath.Join(dst, "sub"))

	writeAt(t, filepath.Join(src, "kept.rs"), []byte("kept"))
	writeAt(t, filepath.Join(dst, "kept.rs"), []byte("kept"))
	// Only the destination has these.
	writeAt(t, filepath.Join(dst, "gone.rs"), []byte("gone"))
	writeAt(t, filepath.Join(dst, "sub", "alsogone.rs"), []byte("gone"))

	err := pruneToMatch(src, dst)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}

	mustBeAbsent(t, filepath.Join(dst, "gone.rs"))
	// Nested, because `fs.SkipDir` returned while visiting a *file* abandons
	// the rest of that file's directory - so the first extra found hid every
	// entry after it, including this one.
	mustBeAbsent(t, filepath.Join(dst, "sub", "alsogone.rs"))

	// And what both have is untouched - the point of the whole exercise.
	if _, err := os.Stat(filepath.Join(dst, "kept.rs")); err != nil { //nolint:noinlineerr // one check
		t.Errorf("a file the source still has was removed: %v", err)
	}
}

// A directory the source no longer has goes with its contents.
func TestSyncRemovesADirectoryTheSourceDropped(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")

	mkdirAt(t, src)
	mkdirAt(t, filepath.Join(dst, "oldcrate", "src"))
	writeAt(t, filepath.Join(dst, "oldcrate", "src", "lib.rs"), []byte("old"))

	err := pruneToMatch(src, dst)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}

	mustBeAbsent(t, filepath.Join(dst, "oldcrate"))
}

// A destination that is not there at all is nothing to prune, which is the
// ordinary first copy and is not an error.
func TestSyncOfAnAbsentDestinationIsQuiet(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := pruneToMatch(dir, filepath.Join(dir, "no-such"))
	if err != nil {
		t.Errorf("pruning an absent destination complained: %v", err)
	}
}

func writeAt(t *testing.T, at string, body []byte) {
	t.Helper()

	err := os.WriteFile(at, body, 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

func mkdirAt(t *testing.T, at string) {
	t.Helper()

	err := os.MkdirAll(at, 0o750)
	if err != nil {
		t.Fatal(err)
	}
}

func touchAt(t *testing.T, at string, when time.Time) {
	t.Helper()

	err := os.Chtimes(at, when, when)
	if err != nil {
		t.Fatal(err)
	}
}

func mustBeAbsent(t *testing.T, at string) {
	t.Helper()

	_, err := os.Stat(at)
	if !os.IsNotExist(err) {
		t.Errorf("%s survived the sync (%v)", at, err)
	}
}

func statOf(t *testing.T, at string) os.FileInfo {
	t.Helper()

	fi, err := os.Stat(at)
	if err != nil {
		t.Fatal(err)
	}

	return fi
}

// An identical file at an identical mode is not touched at all.
//
// **Not even a chmod.** The destination lives in an overlay merged view, and a
// `chmod(2)` on a file whose bytes are in a *lower* layer makes the kernel copy
// the whole file up before applying the mode - so reconciling a mode that was
// already right read and rewrote every byte of every unchanged file. Measured:
// 118 MB of skipped files cost ~236 MB of copy-up on top of the comparison, and
// every one of them landed in the delta the skip existed to keep them out of.
//
// Changing a ctime to the value it already has is work with no result.
func TestAnUnchangedFileAtTheSameModeIsNotTouched(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	same := []byte("identical bytes")
	writeAt(t, src, same)
	writeAt(t, dst, same)

	// 0o600, because a test fixture has no reason to be group-readable and
	// gosec is right to say so.
	chmodTo(t, dst, 0o600)

	act, err := whatSyncMustDo(src, dst, 0o600)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}

	if act != syncNothing {
		t.Errorf("an identical file at the same mode gave %v, wanted %v", act, syncNothing)
	}
}

// Same bytes, different mode: fix the mode and nothing else. The file is not
// rewritten, so it keeps its mtime.
func TestSameBytesAtADifferentModeOnlyChangesTheMode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	same := []byte("identical bytes")
	writeAt(t, src, same)
	writeAt(t, dst, same)

	chmodTo(t, dst, 0o600)

	act, err := whatSyncMustDo(src, dst, 0o700)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}

	if act != syncMode {
		t.Errorf("a mode difference gave %v, wanted %v", act, syncMode)
	}
}

// Different bytes: write, whatever the modes say.
func TestDifferentBytesAreWritten(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	writeAt(t, src, []byte("aaaaBaaaa"))
	writeAt(t, dst, []byte("aaaaAaaaa"))

	act, err := whatSyncMustDo(src, dst, 0o644)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}

	if act != syncWrite {
		t.Errorf("a content difference gave %v, wanted %v", act, syncWrite)
	}
}

// An absent destination is written.
func TestAnAbsentDestinationIsAWrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "src")

	writeAt(t, src, []byte("new"))

	act, err := whatSyncMustDo(src, filepath.Join(dir, "no-such"), 0o644)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}

	if act != syncWrite {
		t.Errorf("an absent destination gave %v, wanted %v", act, syncWrite)
	}
}

func chmodTo(t *testing.T, at string, mode os.FileMode) {
	t.Helper()

	err := os.Chmod(at, mode)
	if err != nil {
		t.Fatal(err)
	}
}
