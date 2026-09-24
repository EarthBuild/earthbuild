package image

import (
	"archive/tar"
	"testing"
)

// An overlayfs deletion is written as the deletion an OCI layer understands.
//
// **The two formats say the same thing and do not look alike.** overlayfs
// records a removed path as a character device 0:0 in the upper directory; an
// OCI layer records it as a regular file named `.wh.<name>`. The store holds the
// first, an image needs the second, and packing the directory verbatim shipped a
// device node - so the next layer that wanted a directory at that path failed to
// unpack: `create directory "w/crates/app/src/": not a directory`.
//
// Measured on the layers of a real image: 19 character devices, 0 `.wh.` entries.
func TestAnOverlayWhiteoutIsPackedAsADeletion(t *testing.T) {
	t.Parallel()

	h := &tar.Header{
		Name:     "w/crates/app/src",
		Typeflag: tar.TypeChar,
		Mode:     0o600,
		Size:     0,
	}

	if !asDeletion(h) {
		t.Fatal("a character device 0:0 was not read as a whiteout")
	}

	if h.Name != "w/crates/app/.wh.src" {
		t.Errorf("named %q, wanted the deletion of src", h.Name)
	}

	if h.Typeflag != tar.TypeReg {
		t.Errorf("still typeflag %q; a deletion is an ordinary empty file", h.Typeflag)
	}
}

// A device node that is not a whiteout is a device node.
//
// `/dev/null` is 1:3 and an image may legitimately carry one; rewriting it into
// a deletion would remove a path the layer meant to create.
func TestARealDeviceIsNotADeletion(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		kind         byte
		major, minor int64
	}{
		{name: "/dev/null", kind: tar.TypeChar, major: 1, minor: 3},
		{name: "a block device", kind: tar.TypeBlock, major: 0, minor: 0},
		{name: "an ordinary file", kind: tar.TypeReg},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := &tar.Header{
				Name: "d/x", Typeflag: tc.kind,
				Devmajor: tc.major, Devminor: tc.minor,
			}

			if asDeletion(h) {
				t.Errorf("%s was rewritten as a deletion", tc.name)
			}

			if h.Name != "d/x" {
				t.Errorf("%s was renamed to %q", tc.name, h.Name)
			}
		})
	}
}

// overlayfs's own bookkeeping does not travel.
//
// `origin` points at an inode in a lower directory that exists only on the
// machine that built the layer, and `impure` is a hint to the kernel that wrote
// it. Neither means anything to whoever pulls the image, and `opaque` means
// something that has to be said differently - see TestAnOpaqueDirectoryIsPacked.
func TestOverlayBookkeepingIsNotShipped(t *testing.T) {
	t.Parallel()

	kept := withoutOverlayAttrs(map[string]string{
		"SCHILY.xattr.user.overlay.origin":    "\x01",
		"SCHILY.xattr.user.overlay.impure":    "y",
		"SCHILY.xattr.trusted.overlay.origin": "\x01",
		"SCHILY.xattr.security.capability":    "cap",
		"SCHILY.xattr.user.mime_type":         "text/plain",
	})

	if _, still := kept["SCHILY.xattr.user.overlay.origin"]; still {
		t.Error("an overlay origin was shipped in the image")
	}

	if len(kept) != 2 {
		t.Errorf("kept %v, wanted the capability and the mime type", kept)
	}

	// The capability especially: a binary that could bind a privileged port
	// during the build must still be able to in the image built from it (E93).
	if _, ok := kept["SCHILY.xattr.security.capability"]; !ok {
		t.Error("the file capability was dropped with the overlay attributes")
	}
}

// A directory that hides everything beneath it says so in the OCI form.
func TestAnOpaqueDirectoryIsPackedAsAMarker(t *testing.T) {
	t.Parallel()

	for _, attr := range []string{
		"SCHILY.xattr.user.overlay.opaque",
		"SCHILY.xattr.trusted.overlay.opaque",
	} {
		if !hidesWhatIsBelow(map[string]string{attr: "y"}) {
			t.Errorf("%s was not read as an opaque directory", attr)
		}
	}

	// "n" is the spelling for "no longer opaque", and is not a marker.
	if hidesWhatIsBelow(map[string]string{"SCHILY.xattr.user.overlay.opaque": "n"}) {
		t.Error("opaque=n was read as opaque")
	}

	if hidesWhatIsBelow(map[string]string{"SCHILY.xattr.user.overlay.impure": "y"}) {
		t.Error("impure was read as opaque")
	}
}
