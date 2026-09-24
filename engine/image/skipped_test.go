package image_test

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// An entry the machine cannot create is skipped, not half-created.
//
// `dev/console` is a character device and every Debian-derived base image
// carries one. Creating it needs `CAP_MKNOD` in the *initial* user namespace,
// which a rootless build does not have, so `makeSpecial` leaves it out - which
// is right, and is the fix E91 made after the same `default:` branch in
// `copyTree` lost every deletion this engine ever made.
//
// `setMeta` then ran anyway:
//
//	FROM maven:3.8.5-openjdk-17: layer 0: set mode on "dev/console":
//	chmod .../.pulling-1806681926/dev/console: no such file or directory
//
// Two of twelve corpus targets, and the message describes a missing file rather
// than an unavailable capability - so a reader concludes the archive is corrupt.
//
// **The failure class, third instance: a decision made in one place and not told
// to its own follow-up.** E106 was a shared definition with one consumer left
// behind; E107 a fix applied to one of two implementations of an interface; this
// is a conditional creation with an unconditional next step, eight lines apart.
// And the correct signature was already written in the sibling implementation -
// `guest.copySpecial` returns `(placed bool, err error)` precisely so its caller
// can tell.
func TestAnUncreatableEntryIsSkippedWholly(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("running as root, which can make the device and so never reaches the skip")
	}

	var buf bytes.Buffer

	w := tar.NewWriter(&buf)

	// Exactly what a Debian base image's first layer holds.
	err := w.WriteHeader(&tar.Header{
		Typeflag: tar.TypeChar, Name: "dev/console", Mode: 0o600,
		Devmajor: 5, Devminor: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// A plain file after it, so a failure that stops the walk is visible as a
	// missing file rather than only as an error.
	err = w.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "etc/hosts", Mode: 0o644, Size: 4})
	if err != nil {
		t.Fatal(err)
	}

	_, err = w.Write([]byte("body"))
	if err != nil {
		t.Fatal(err)
	}

	err = w.Close()
	if err != nil {
		t.Fatal(err)
	}

	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(parent, "layer")

	err = image.Unpack(bytes.NewReader(buf.Bytes()), out)
	if err != nil {
		t.Fatalf("a base image carrying a device this machine may not create"+
			" failed to unpack:\n  %v", err)
	}

	_, err = os.Lstat(filepath.Join(out, "etc", "hosts"))
	if err != nil {
		t.Errorf("the entries after the device did not arrive: %v", err)
	}
}
