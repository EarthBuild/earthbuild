package image_test

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/image"
	"golang.org/x/sys/unix"
)

// tarOf builds a one-entry archive from a header and its body.
func oneEntry(t *testing.T, h *tar.Header, body string) []byte {
	t.Helper()

	var buf bytes.Buffer

	w := tar.NewWriter(&buf)

	h.Size = int64(len(body))

	err := w.WriteHeader(h)
	if err != nil {
		t.Fatal(err)
	}

	if body != "" {
		_, err = w.Write([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
	}

	err = w.Close()
	if err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

// unpackInto unpacks an archive and returns where it went.
func unpackInto(t *testing.T, archive []byte) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "layer")

	err := image.Unpack(bytes.NewReader(archive), dir)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}

	return dir
}

// What a base image's archive says, the unpacked layer has.
//
// The same question E91 asked of `copyTree`, aimed at the other implementation
// of the same idea. `image/unpack.go` writes **every base image** into the
// store, and its type switch ends:
//
//	default:
//	    // Character devices, fifos and sockets need privilege this may not
//	    // have, and a base image rarely carries one. Skipped rather than
//	    // failed, and named here so the omission is deliberate.
//
// Which is word for word the shape of `copyTree`'s branch that lost every
// deletion this engine ever made (E88). **Two copies of one piece of wrong
// reasoning, in two files, each documented as deliberate.**
//
// Green paper §3.3 lists what a layer records. A tar header carries all of it -
// mode, uid, gid, link target, link identity, xattrs in PAX records, mtime,
// device numbers - so there is no excuse of the format's making, and each
// property below is a thing the archive stated and the tree either has or does
// not.
func TestUnpackReproducesWhatTheArchiveStates(t *testing.T) {
	t.Parallel()

	t.Run("mode", func(t *testing.T) {
		t.Parallel()

		dir := unpackInto(t, oneEntry(t, &tar.Header{
			Typeflag: tar.TypeReg, Name: "exec", Mode: 0o755,
		}, "body"))

		fi, err := os.Lstat(filepath.Join(dir, "exec"))
		if err != nil {
			t.Fatal(err)
		}

		if fi.Mode().Perm() != 0o755 {
			t.Errorf("the archive says 0755 and the file is %o", fi.Mode().Perm())
		}
	})

	t.Run("gid", func(t *testing.T) {
		t.Parallel()

		gid := secondaryGroup(t)

		dir := unpackInto(t, oneEntry(t, &tar.Header{
			Typeflag: tar.TypeReg, Name: "owned", Mode: 0o644,
			Uid: os.Getuid(), Gid: gid,
		}, "body"))

		fi, err := os.Lstat(filepath.Join(dir, "owned"))
		if err != nil {
			t.Fatal(err)
		}

		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			t.Skip("this platform does not report ownership")
		}

		if int(st.Gid) != gid {
			t.Errorf("the archive says group %d and the file is in %d", gid, st.Gid)
		}
	})

	t.Run("symlink target", func(t *testing.T) {
		t.Parallel()

		dir := unpackInto(t, oneEntry(t, &tar.Header{
			Typeflag: tar.TypeSymlink, Name: "link", Linkname: "elsewhere",
		}, ""))

		got, err := os.Readlink(filepath.Join(dir, "link"))
		if err != nil {
			t.Fatal(err)
		}

		if got != "elsewhere" {
			t.Errorf("the link points at %q", got)
		}
	})

	t.Run("xattrs", func(t *testing.T) {
		t.Parallel()

		// PAX records are how a tar carries them, and how `setcap` survives a
		// registry: a binary with cap_net_bind_service has the grant in
		// `security.capability` and an image that lost it has a service that
		// cannot bind its port.
		dir := unpackInto(t, oneEntry(t, &tar.Header{
			Typeflag: tar.TypeReg, Name: "labelled", Mode: 0o644, Format: tar.FormatPAX,
			PAXRecords: map[string]string{"SCHILY.xattr.user.earthbuild.probe": testValue},
		}, "body"))

		buf := make([]byte, 64)

		n, err := unix.Lgetxattr(filepath.Join(dir, "labelled"), "user.earthbuild.probe", buf)
		if err != nil {
			t.Errorf("the archive carries an extended attribute and the file has none: %v", err)

			return
		}

		if string(buf[:n]) != testValue {
			t.Errorf("the attribute reads %q", buf[:n])
		}
	})

	t.Run("mtime", func(t *testing.T) {
		t.Parallel()

		at := time.Unix(1_600_000_000, 0)

		dir := unpackInto(t, oneEntry(t, &tar.Header{
			Typeflag: tar.TypeReg, Name: "stamped", Mode: 0o644, ModTime: at,
		}, "body"))

		fi, err := os.Lstat(filepath.Join(dir, "stamped"))
		if err != nil {
			t.Fatal(err)
		}

		if !fi.ModTime().Equal(at) {
			t.Errorf("the archive says %v and the file says %v", at, fi.ModTime())
		}
	})

	t.Run("a fifo", func(t *testing.T) {
		t.Parallel()

		dir := unpackInto(t, oneEntry(t, &tar.Header{
			Typeflag: tar.TypeFifo, Name: "pipe", Mode: 0o600,
		}, ""))

		fi, err := os.Lstat(filepath.Join(dir, "pipe"))
		if err != nil {
			t.Errorf("the archive carries a fifo and the layer has nothing there: %v", err)

			return
		}

		if fi.Mode()&os.ModeNamedPipe == 0 {
			t.Errorf("the entry is %s, not a fifo", fi.Mode().Type())
		}
	})
}

func secondaryGroup(t *testing.T) int {
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

	t.Skip("this process belongs to one group")

	return 0
}
