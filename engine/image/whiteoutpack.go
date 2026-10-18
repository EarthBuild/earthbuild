package image

import (
	"archive/tar"
	"path"
	"strings"
)

// overlayAttrs is the prefix overlayfs keeps its bookkeeping under, in the two
// namespaces it uses: `trusted` where the mount is privileged, `user` where it
// is not (`userxattr`).
const overlayAttrs = "overlay."

// asDeletion rewrites an overlayfs whiteout into the deletion an OCI layer
// means by it, and reports whether the entry was one.
//
// **The two formats say the same thing and look nothing alike.** overlayfs
// records a removed path as a character device 0:0 in the upper directory; an
// image records it as an empty file named `.wh.<name>` beside where the path
// was. The store holds the first because that is what the kernel wrote, and
// packing the directory verbatim shipped the device node - so the next layer
// wanting a directory there could not unpack: `create directory
// "w/crates/app/src/": not a directory`, on an image that had built fine.
//
// **0:0 and not merely "a character device".** `/dev/null` is 1:3 and an image
// may carry one; rewriting that into a deletion would remove a path the layer
// meant to create. The device numbers are the whole of the test overlayfs
// itself uses.
func asDeletion(h *tar.Header) bool {
	if h.Typeflag != tar.TypeChar || h.Devmajor != 0 || h.Devminor != 0 {
		return false
	}

	dir, base := path.Split(h.Name)

	h.Name = dir + whPrefix + base
	h.Typeflag = tar.TypeReg
	h.Size = 0
	h.Mode = 0
	h.Devmajor, h.Devminor = 0, 0

	return true
}

// hidesWhatIsBelow reports that a directory is opaque: it hides everything the
// layers under it put at that path, rather than merging with them.
//
// The marker is an attribute in the store and an entry in an image - see
// opaqueEntry - which is the same translation `asDeletion` performs, for the
// other half of what a delete looks like. `rm -rf d && mkdir d` produces one.
func hidesWhatIsBelow(xs map[string]string) bool {
	for k, v := range xs {
		if strings.HasSuffix(k, overlayAttrs+"opaque") && v == "y" {
			return true
		}
	}

	return false
}

// opaqueEntry is the marker that says a directory hides what is beneath it.
//
// A child of the directory rather than a property of it, because that is how an
// image spells it: the entry sorts before the directory's contents, and the
// unpacker clears what is there when it reaches it.
func opaqueEntry(dir string, when *tar.Header) *tar.Header {
	return &tar.Header{
		Name:       path.Join(dir, whOpaque),
		Typeflag:   tar.TypeReg,
		Mode:       0,
		Size:       0,
		ModTime:    when.ModTime,
		AccessTime: when.AccessTime,
		ChangeTime: when.ChangeTime,
		Format:     tar.FormatPAX,
	}
}

// withoutOverlayAttrs drops the attributes that belong to the overlay a layer
// was captured from, and keeps everything else.
//
// `origin` names an inode in a lower directory that exists only on the machine
// that wrote it, `impure` is a hint to the kernel that wrote it, and `opaque` is
// said as an entry instead. None of the three means anything to whoever pulls
// the image, and a runtime that acts on one is acting on another machine's
// bookkeeping.
//
// Everything else stays. `security.capability` in particular: a binary that
// could bind a privileged port during the build has to be able to in the image
// built from it (E93).
func withoutOverlayAttrs(xs map[string]string) map[string]string {
	kept := make(map[string]string, len(xs))

	for k, v := range xs {
		if strings.Contains(k, overlayAttrs) {
			continue
		}

		kept[k] = v
	}

	return kept
}
