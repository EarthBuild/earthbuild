package exec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ignore"
)

// The bytes staged into a context are the bytes its identity was computed over.
//
// **They were not.** The context's digest is taken with the ignore file applied
// (`engine/interp`, `excluderFor`), and `stageContext` copied the directory with
// `copyDir`, which consults nothing. So `.earthlyignore` decided the cache key
// and did not decide what the container got.
//
// It is a correctness gap before it is a slow one: a context whose contents do
// not match its own identity is a layer nothing downstream can reason about.
// The cost is visible too - this repository generates about sixty thousand test
// fixture files into gitignored `testdata/`, named by `.earthlyignore`, and
// every native build copied all of them. That is what exhausted the machine's
// file table and took a build down with `ENFILE` (E622, E623).
func TestAStagedContextLeavesOutWhatTheIgnoreFileNames(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.WriteFile(filepath.Join(root, ".earthlyignore"), []byte("**/testdata/bigtree-*\nbuild\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{
		"engine/store/testdata/bigtree-20000/d01/f1",
		"engine/store/testdata/keep.txt",
		"engine/store/store.go",
		"build/output.bin",
	} {
		p := filepath.Join(root, rel)

		err := os.MkdirAll(filepath.Dir(p), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(p, []byte("x"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	dst := filepath.Join(t.TempDir(), "staged")

	err = copyDirExcluding(filepath.Join(root, "engine"), dst, ignore.For(root, filepath.Join(root, "engine")))
	if err != nil {
		t.Fatal(err)
	}

	// Named by the ignore file, so it must not be here.
	_, err = os.Lstat(filepath.Join(dst, "store/testdata/bigtree-20000/d01/f1"))
	if err == nil {
		t.Error("a file the ignore file names was staged into the context")
	}

	// Everything else must be, or the fix drops source instead of fixtures -
	// which is the failure mode `.earthlyignore` warns about in its own comments.
	for _, rel := range []string{"store/testdata/keep.txt", "store/store.go"} {
		_, err := os.Lstat(filepath.Join(dst, rel))
		if err != nil {
			t.Errorf("%s was left out of the context, and a build may read it", rel)
		}
	}
}

// The prefix matters: an excluder built for a subdirectory has to test the path
// the ignore file speaks about, which is relative to the context root.
func TestAnExcluderSpeaksThePathsTheIgnoreFileDoes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.WriteFile(filepath.Join(root, ".earthlyignore"), []byte("**/testdata/bigtree-*\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	ex := ignore.For(root, filepath.Join(root, "engine"))

	if !ex.Excludes("store/testdata/bigtree-20000") {
		t.Error("a path under the walk's own root was not matched against the ignore file")
	}

	if ex.Excludes("store/store.go") {
		t.Error("an ordinary source file was excluded")
	}
}
