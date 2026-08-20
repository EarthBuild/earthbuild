package guest

import (
	"os"
	"path/filepath"
	"testing"
)

// A tree containing a directory nothing may write to is copied whole.
//
// The fourth place this engine has met the same rule, and it was found the same
// way as the other three: by building a real image. `maven:3.8.5-openjdk-17`
// has `/root` at 0700 and a step that writes `/root/.m2` inside it - and
// capturing the step's result created the directory with its declared mode and
// then could not put anything in it.
//
// A directory's mode describes the tree, not the copying of it. It is applied
// once everything is in place, deepest first.
func TestATreeWithAnUnwritableDirectoryIsCopied(t *testing.T) {
	t.Parallel()

	src := t.TempDir()

	inner := filepath.Join(src, "root", "cache")
	err := os.MkdirAll(inner, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(inner, "file"), []byte("x\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// Restrictive on the way out, so the copy meets it before its contents.
	err = os.Chmod(filepath.Join(src, "root"), 0o500)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Chmod(filepath.Join(src, "root"), 0o700) })

	dst := filepath.Join(t.TempDir(), "out")

	err = copyTree(src, dst, copyOpts{})
	if err != nil {
		t.Fatalf("a tree with a read-only directory was not copied: %v", err)
	}

	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dst, "root"), 0o700) })

	_, err = os.Stat(filepath.Join(dst, "root", "cache", "file"))
	if err != nil {
		t.Fatalf("the file inside it is missing: %v", err)
	}

	fi, err := os.Stat(filepath.Join(dst, "root"))
	if err != nil {
		t.Fatal(err)
	}

	if fi.Mode().Perm() != 0o500 {
		t.Errorf("the directory ended up %o, want the 500 it had", fi.Mode().Perm())
	}
}
