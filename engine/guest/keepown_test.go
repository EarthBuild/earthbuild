package guest

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// otherGroup finds a group this process belongs to that is not its primary one.
//
// Ownership is what is under test and only root may hand a file to an arbitrary
// user, so the test moves it somewhere it is already allowed to: a secondary
// group. That keeps the check discriminating without needing privilege - the
// copy either carried the gid across or it did not.
func otherGroup(t *testing.T) int {
	t.Helper()

	groups, err := os.Getgroups()
	if err != nil {
		t.Skipf("cannot read this process's groups: %v", err)
	}

	for _, g := range groups {
		if g != os.Getgid() {
			return g
		}
	}

	t.Skip("this process belongs to one group, so there is nothing to move a file to")

	return 0
}

// needsOwnershipStore skips when this machine's store cannot carry ownership.
//
// The same question the copy asks before it runs, asked by the test for the
// same reason: on a macOS host the layer store is a shared directory that maps
// every uid to the invoking user, so there is no configuration in which these
// assertions could hold. Skipped with the reason rather than weakened to
// something that passes anywhere - a test that passes everywhere and checks
// ownership nowhere is the outcome this whole file exists to avoid.
func needsOwnershipStore(t *testing.T, dir string) {
	t.Helper()

	err := checkStoreOwnership(dir, os.Lchown)
	if err != nil {
		t.Skipf("this store cannot carry ownership: %v", err)
	}
}

func gidOf(t *testing.T, path string) int {
	t.Helper()

	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}

	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("this platform does not report ownership")
	}

	return int(st.Gid)
}

// `COPY --keep-own` carries a file's ownership across.
//
// Measured before implemented. With the flag the reference delivers a file
// `chown`ed to 65534 as 65534; without it, **both engines deliver root**, so
// the default agreed all along and the flag was the only thing missing (E34).
//
// It is ownership *inside an image*, which is why this is worth having: a
// service that drops privileges to the user its files belong to fails at
// runtime, in a container, a long way from the COPY that flattened the uid to
// zero - and produces no build-time symptom at all.
func TestKeepOwnCarriesOwnershipAcross(t *testing.T) {
	t.Parallel()

	s, h, layerRoot := linkDirFixture(t)

	needsOwnershipStore(t, s.LayerDir)

	gid := otherGroup(t)
	src := filepath.Join(layerRoot, "owned.txt")

	err := os.WriteFile(src, []byte("body\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Lchown(src, os.Getuid(), gid)
	if err != nil {
		t.Skipf("cannot change this file's group: %v", err)
	}

	err = s.copyIn(h, []string{testSrcLayer}, "owned.txt", "/got.txt", copyOpts{KeepOwn: true})
	if err != nil {
		t.Fatal(err)
	}

	if got := gidOf(t, filepath.Join(h.root, "got.txt")); got != gid {
		t.Errorf("the copy landed in group %d, not %d", got, gid)
	}
}

// Without the flag, ownership is not carried - which is the default both
// engines already agreed on.
//
// The arm that stops the feature being implemented by preserving everything
// always. `COPY` into an image is not a backup: the reference flattens
// ownership unless asked, and a build that quietly differed here would produce
// images whose files belong to a uid that exists on the building machine and
// nowhere else.
func TestWithoutKeepOwnTheGroupIsNotCarried(t *testing.T) {
	t.Parallel()

	s, h, layerRoot := linkDirFixture(t)

	needsOwnershipStore(t, s.LayerDir)

	gid := otherGroup(t)
	src := filepath.Join(layerRoot, "owned.txt")

	err := os.WriteFile(src, []byte("body\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Lchown(src, os.Getuid(), gid)
	if err != nil {
		t.Skipf("cannot change this file's group: %v", err)
	}

	err = s.copyIn(h, []string{testSrcLayer}, "owned.txt", "/got.txt", copyOpts{})
	if err != nil {
		t.Fatal(err)
	}

	if got := gidOf(t, filepath.Join(h.root, "got.txt")); got == gid {
		t.Error("ownership was carried without the flag asking for it")
	}
}

// A whole tree keeps its ownership, entry by entry.
//
// The half that a file-only implementation would miss, and the half that
// matters in practice: `COPY --keep-own --dir app /srv` is the shape this flag
// appears in, and a tree whose root kept its group while everything inside it
// reverted would be worse than one that dropped it consistently - the failure
// would be per-file and look like corruption rather than like a missing flag.
func TestKeepOwnCarriesOwnershipThroughATree(t *testing.T) {
	t.Parallel()

	s, h, layerRoot := linkDirFixture(t)

	needsOwnershipStore(t, s.LayerDir)

	gid := otherGroup(t)

	for _, p := range []string{"real", "real/a.txt"} {
		err := os.Lchown(filepath.Join(layerRoot, p), os.Getuid(), gid)
		if err != nil {
			t.Skipf("cannot change this file's group: %v", err)
		}
	}

	err := s.copyIn(h, []string{testSrcLayer}, "real", "/got", copyOpts{AsDir: true, KeepOwn: true})
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"got", "got/a.txt"} {
		if got := gidOf(t, filepath.Join(h.root, p)); got != gid {
			t.Errorf("%s landed in group %d, not %d", p, got, gid)
		}
	}
}

// A symlink's own ownership is carried, not its target's.
//
// `os.Chown` follows a link and `os.Lchown` does not, and the difference is a
// step that changes the ownership of something it was only supposed to copy -
// in the *source* layer, which is shared and which the next build reads.
func TestKeepOwnUsesLchownForALink(t *testing.T) {
	t.Parallel()

	s, h, layerRoot := linkDirFixture(t)

	needsOwnershipStore(t, s.LayerDir)

	gid := otherGroup(t)

	err := os.Lchown(filepath.Join(layerRoot, "real", "a.txt"), os.Getuid(), os.Getgid())
	if err != nil {
		t.Skipf("cannot change this file's group: %v", err)
	}

	err = os.Lchown(filepath.Join(layerRoot, "link"), os.Getuid(), gid)
	if err != nil {
		t.Skipf("cannot change this link's group: %v", err)
	}

	err = s.copyIn(h, []string{testSrcLayer}, "link", "/got",
		copyOpts{AsDir: true, KeepOwn: true, NoFollow: true})
	if err != nil {
		t.Fatal(err)
	}

	if got := gidOf(t, filepath.Join(h.root, "got")); got != gid {
		t.Errorf("the link landed in group %d, not %d", got, gid)
	}

	// And the source is untouched: a copy that chowned through the link would
	// have moved the target's group in a layer the next build will read.
	if got := gidOf(t, filepath.Join(layerRoot, "real", "a.txt")); got != os.Getgid() {
		t.Errorf("the copy changed the group of something in the source layer, to %d", got)
	}
}
