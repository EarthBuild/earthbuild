package image

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestResolvingEntriesDoesNotWalkTheTreeForEachOne.
//
// **15.5 `newfstatat` per entry**, counted with `strace -c` over
// `golang:1.26-alpine`'s largest layer: 232302 of them for 16703 entries, where
// the writes themselves are one open, one chmod, one utimes and one close each.
//
// They are all in the escape check. `filepath.EvalSymlinks` lstats every
// component of what it is given, and this resolved *two* paths per entry - the
// root, which cannot change during an unpack and was already resolved before
// the walk began, and the entry's parent, which fifteen thousand entries share
// a few thousand of.
//
// The check itself is not negotiable: an archive can write `link -> /tmp` and
// then `link/x`, which contains no `..` and lands outside the layer, and
// refusing that is what `safePath` is for (E628). What is negotiable is
// resolving the same parent for every file in it.
//
// Only a symlink can change what a path resolves to. Creating a directory or a
// file cannot, so what is remembered stays true until the archive plants one -
// and the unpacker knows when it does, because it is the one creating it.
func TestResolvingEntriesDoesNotWalkTheTreeForEachOne(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.MkdirAll(filepath.Join(root, "usr", "local", "go", "src"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	r := newRooted(root)

	// Counted after construction, so resolving the root once is not the thing
	// being measured - resolving it again for every entry is.
	calls := 0
	r.eval = func(p string) (string, error) {
		calls++

		return filepath.EvalSymlinks(p)
	}

	const entries = 200

	for i := range entries {
		_, perr := r.path(fmt.Sprintf("usr/local/go/src/f%03d", i))
		if perr != nil {
			t.Fatalf("entry %d was refused: %v", i, perr)
		}
	}

	if calls > 2 {
		t.Errorf("%d entries under one directory took %d resolutions, want at most 2"+
			"\n  every one of them walks the whole path, and that was 15.5 stats"+
			"\n  per entry on a real layer", entries, calls)
	}

	// And what was remembered is dropped when a symlink could have changed it.
	r.forget()

	_, err = r.path("usr/local/go/src/again")
	if err != nil {
		t.Fatal(err)
	}

	if calls < 2 {
		t.Errorf("after forgetting, resolution did not happen again (%d calls)"+
			"\n  a cache that survives a planted symlink is the escape it exists"+
			"\n  to refuse", calls)
	}
}

// A symlink lands inside the root even where nothing checked its parent.
//
// `path` refusing a parent that resolves out of the root is the guard. This is
// the floor under it: the link is placed through an `os.Root`, whose walk will
// not leave the root, so a stale `dirs` or a caller that skipped `path` still
// cannot plant one outside. The link's *target* is not checked, and must not
// be - `/bin/sh -> /busybox` is resolved inside the step's root at run time.
func TestASymlinkIsPlacedInsideTheRootEvenUnchecked(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")

	for _, d := range []string{filepath.Join(root, "bin"), outside} {
		err := os.MkdirAll(d, 0o750)
		if err != nil {
			t.Fatal(err)
		}
	}

	err := os.Symlink(outside, filepath.Join(root, "esc"))
	if err != nil {
		t.Fatal(err)
	}

	r := newRooted(root)
	defer r.close()

	err = r.symlink("/busybox", filepath.Join(root, "bin", "sh"))
	if err != nil {
		t.Fatalf("a link pointing out of the root was refused: %v"+
			"\n  its target is resolved inside the step's root at run time", err)
	}

	got, err := os.Readlink(filepath.Join(root, "bin", "sh"))
	if err != nil || got != "/busybox" {
		t.Errorf("bin/sh reads %q (%v), want /busybox", got, err)
	}

	err = r.symlink("/etc", filepath.Join(root, "esc", "planted"))
	if err == nil {
		t.Error("a link was placed through esc -> " + outside +
			"\n  its parent resolves out of the root, so it landed outside it")
	}

	_, err = os.Lstat(filepath.Join(outside, "planted"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s/planted exists (%v): the link was written outside the root", outside, err)
	}
}
