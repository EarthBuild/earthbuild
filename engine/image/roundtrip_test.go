package image_test

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
	"golang.org/x/sys/unix"
)

// roundTrip packs a directory and unpacks it again.
func roundTrip(t *testing.T, dir string) string {
	t.Helper()

	var buf bytes.Buffer

	_, _, err := image.Pack(dir, &buf)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}

	// Resolved, because macOS puts a test's temporary directory under
	// `/var/folders`, `/var` is a symlink to `/private/var`, and the unpacker
	// refuses to write through a symlink out of the layer - correctly, and
	// against the fixture rather than the code. Two of this test's first three
	// failures were that, and neither was a defect.
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(parent, "back")

	err = image.Unpack(bytes.NewReader(buf.Bytes()), out)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}

	return out
}

// What a layer carries survives being packed and unpacked again.
//
// The third implementation of "write a layer", and the strongest form of the
// question: `Pack` and `Unpack` are inverses by construction - the doc comment
// says so - so composing them must be the identity on everything green paper
// §3.3 records. It needs no oracle and no fixture beyond one of each kind of
// file, and it covers both directions at once.
//
// `Pack` turns out to serve two callers with one set of rules. `writeLayers`
// packs each **layer** of an image; `packimage` packs the OCI **layout
// directory** - blobs and an index this engine has just written. For the second,
// *"a timestamp and an owner are properties of the checkout, not of what was
// built"* is exactly right. For the first it is not: ownership inside a layer is
// what a `RUN chown` put there.
//
// Times are normalised deliberately in both cases and this test does not fight
// that, because two builds of one input must produce one image. Ownership is a
// live trade-off and is recorded rather than decided here. The rest -
// attributes, hard links, special files - has no reproducibility argument at all
// and is simply lost.
func TestALayerSurvivesPackAndUnpack(t *testing.T) {
	t.Parallel()

	t.Run("mode", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		write(t, filepath.Join(dir, "exec"), 0o755)

		fi, err := os.Lstat(filepath.Join(roundTrip(t, dir), "exec"))
		if err != nil {
			t.Fatal(err)
		}

		if fi.Mode().Perm() != 0o755 {
			t.Errorf("the mode came back as %o", fi.Mode().Perm())
		}
	})

	t.Run("symlink target", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		write(t, filepath.Join(dir, "x"), 0o600)

		err := os.Symlink("x", filepath.Join(dir, "link"))
		if err != nil {
			t.Skipf("symlinks are not available here: %v", err)
		}

		got, err := os.Readlink(filepath.Join(roundTrip(t, dir), "link"))
		if err != nil {
			t.Fatal(err)
		}

		if got != "x" {
			t.Errorf("the link came back pointing at %q", got)
		}
	})

	t.Run("xattrs", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		p := filepath.Join(dir, "labelled")
		write(t, p, 0o600)

		err := unix.Lsetxattr(p, "user.earthbuild.probe", []byte(testValue), 0)
		if err != nil {
			t.Skipf("this filesystem does not take extended attributes: %v", err)
		}

		buf := make([]byte, 64)

		n, err := unix.Lgetxattr(filepath.Join(roundTrip(t, dir), "labelled"),
			"user.earthbuild.probe", buf)
		if err != nil {
			t.Errorf("the attribute did not survive the round trip: %v", err)

			return
		}

		if string(buf[:n]) != testValue {
			t.Errorf("the attribute came back as %q", buf[:n])
		}
	})

	t.Run("hardlink identity", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		write(t, filepath.Join(dir, "a"), 0o600)

		err := os.Link(filepath.Join(dir, "a"), filepath.Join(dir, "b"))
		if err != nil {
			t.Skipf("hard links are not available here: %v", err)
		}

		back := roundTrip(t, dir)

		a, err := os.Lstat(filepath.Join(back, "a"))
		if err != nil {
			t.Fatal(err)
		}

		b, err := os.Lstat(filepath.Join(back, "b"))
		if err != nil {
			t.Fatal(err)
		}

		if !os.SameFile(a, b) {
			t.Error("two names for one file came back as two files")
		}
	})

	t.Run("a fifo", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()

		err := unix.Mkfifo(filepath.Join(dir, "pipe"), 0o600)
		if err != nil {
			t.Skipf("this machine cannot make a fifo: %v", err)
		}

		fi, err := os.Lstat(filepath.Join(roundTrip(t, dir), "pipe"))
		if err != nil {
			t.Errorf("the fifo did not survive the round trip: %v", err)

			return
		}

		if fi.Mode()&os.ModeNamedPipe == 0 {
			t.Errorf("it came back as %s", fi.Mode().Type())
		}
	})
}

// Ownership is normalised on purpose, and this pins the decision.
//
// `Pack` zeroes uid and gid so that two builds of one input produce one image,
// which is the property an image's identity rests on. It costs fidelity: a
// layer whose files a `RUN chown` gave to `nobody` ships as root's.
//
// The trade-off is real in both directions and belongs to a maintainer, so it
// is recorded as a test rather than argued in a comment. **If somebody makes
// packing carry ownership, this fails and asks whether cross-machine
// reproducibility was considered** - which is the question, and it is easy to
// answer for one machine and forget for a fleet.
func TestPackingNormalisesOwnershipDeliberately(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "owned")
	write(t, p, 0o600)

	gid := secondaryGroup(t)

	err := os.Lchown(p, os.Getuid(), gid)
	if err != nil {
		t.Skipf("cannot change this file's group: %v", err)
	}

	fi, err := os.Lstat(filepath.Join(roundTrip(t, dir), "owned"))
	if err != nil {
		t.Fatal(err)
	}

	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("this platform does not report ownership")
	}

	if int(st.Gid) == gid && gid != os.Getgid() {
		t.Errorf("packing now carries ownership, which it normalised for reproducibility:"+
			"\n  the group %d survived the round trip"+
			"\n  if that is deliberate, two builds on two machines must still produce one image", gid)
	}
}

func write(t *testing.T, p string, mode os.FileMode) {
	t.Helper()

	err := os.WriteFile(p, []byte("body\n"), mode)
	if err != nil {
		t.Fatal(err)
	}

	// os.WriteFile applies the umask; the mode is what the test asked for.
	err = os.Chmod(p, mode)
	if err != nil {
		t.Fatal(err)
	}
}
