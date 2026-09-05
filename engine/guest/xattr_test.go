package guest

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// setXattr sets one, skipping where the filesystem will not take it.
func setXattr(t *testing.T, path, name string, value []byte) {
	t.Helper()

	err := unix.Lsetxattr(path, name, value, 0)
	if err != nil {
		t.Skipf("this filesystem does not take extended attributes: %v", err)
	}
}

func readXattr(t *testing.T, path, name string) []byte {
	t.Helper()

	buf := make([]byte, 128)

	n, err := unix.Lgetxattr(path, name, buf)
	if err != nil {
		return nil
	}

	return buf[:n]
}

// An extended attribute survives a copy.
//
// The fourth thing `copyTree` dropped, found by the same question as the
// whiteouts (E88) and the hard links (E89): what does this code discard?
//
// `layer.Take` reads every xattr on every entry and hashes it - green paper
// §3.3 lists xattrs among the metadata a layer records - and `copyTree` carried
// none of them for a regular file. The one it did carry was added last
// iteration and only for directories, because that is the one the overlay uses
// to mark a directory opaque.
//
// The case that makes this matter is `setcap`. A binary given
// `cap_net_bind_service` carries it in `security.capability`, and a copy that
// drops it produces an image whose service cannot bind its port - at runtime,
// in a container, from a build that reported success. Ownership and mode are
// carried carefully by this function and the third thing a POSIX file's
// authority rests on was not.
func TestAnExtendedAttributeSurvivesACopy(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	file := filepath.Join(src, "a.txt")

	err := os.WriteFile(file, []byte("body\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// `user.` because it is the namespace an unprivileged process may write.
	// `security.capability` is the one that matters and needs root, and the
	// mechanism is the same for both - the guest runs as root where it counts.
	setXattr(t, file, "user.earthbuild.test", []byte("kept"))

	err = copyTree(src, dst, copyOpts{})
	if err != nil {
		t.Fatal(err)
	}

	if got := readXattr(t, filepath.Join(dst, "a.txt"), "user.earthbuild.test"); string(got) != "kept" {
		t.Errorf("the extended attribute did not survive the copy: %q", got)
	}
}

// A directory's attributes survive too, and not only the overlay's own.
//
// The previous iteration carried exactly two names, both of them the overlay's
// opaque marker, because that was what the bug in front of it needed. A
// directory can carry any of them - SELinux labels sit on directories as much
// as on files - and a rule that names the two attributes somebody happened to
// need is the same shape as a `default:` branch that skips devices because they
// "rarely appear".
func TestADirectorysAttributesSurviveACopy(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	inner := filepath.Join(src, "d")

	err := os.MkdirAll(inner, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	setXattr(t, inner, "user.earthbuild.dir", []byte("kept"))

	err = copyTree(src, dst, copyOpts{})
	if err != nil {
		t.Fatal(err)
	}

	if got := readXattr(t, filepath.Join(dst, "d"), "user.earthbuild.dir"); string(got) != "kept" {
		t.Errorf("the directory's extended attribute did not survive the copy: %q", got)
	}
}

// A symlink's own ownership is carried, on a copy that can carry it.
//
// This branch returned bare `nil` until now. Ownership was meant to have been
// added to it two iterations ago and a scripted edit whose search text did not
// match wrote nothing and reported nothing - and the test that would have
// caught it, `TestKeepOwnUsesLchownForALink`, skips on a store that cannot
// carry ownership, which is every macOS host.
//
// **A silent no-op edit plus a test that skips is indistinguishable from a
// feature that works.** The edit said nothing, the test said SKIP, and the gate
// was green. This one uses a group the process already belongs to, which macOS
// does allow, so the branch is exercised where the other could not be.
func TestASymlinksOwnershipIsCarried(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	gid := otherGroup(t)

	err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("body\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink("a.txt", filepath.Join(src, "link"))
	if err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	err = os.Lchown(filepath.Join(src, "link"), os.Getuid(), gid)
	if err != nil {
		t.Skipf("cannot change this link's group: %v", err)
	}

	err = copyTree(src, dst, copyOpts{KeepOwn: true})
	if err != nil {
		t.Skipf("this copy cannot carry ownership: %v", err)
	}

	if got := gidOf(t, filepath.Join(dst, "link")); got != gid {
		t.Errorf("the link landed in group %d, not %d", got, gid)
	}
}
