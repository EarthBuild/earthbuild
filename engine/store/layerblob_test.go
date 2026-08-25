package store

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestALayerRemembersTheBlobItCameFrom.
//
// **The join a lazy pull turns on.** A blob is named by the hash of its
// compressed bytes and a layer by the hash of the tree it unpacks to, and
// nothing relates the two except the pull that saw both. Without the note, a
// store holding a perfectly good 61MB blob has no way to know which of its
// layers it is - so it unpacks the layer again, from the network.
//
// Beside the layer, as `.unmarked` and the configuration are, and for the same
// reason: it is a fact about that layer and it should go away when the layer
// does.
func TestALayerRemembersTheBlobItCameFrom(t *testing.T) {
	t.Parallel()

	d := DirStore(t.TempDir())

	layer := ir.NodeID{1, 2, 3}
	at := ir.NodeID{4, 5, 6}

	_, _, ok := d.BlobOf(layer)
	if ok {
		t.Fatal("a layer nobody has pulled claims to know its blob")
	}

	d.NoteBlob(layer, at, "application/vnd.oci.image.layer.v1.tar+gzip")

	got, mediaType, ok := d.BlobOf(layer)
	if !ok {
		t.Fatal("the note was written and cannot be read back")
	}

	if got != at {
		t.Errorf("the layer names blob %v, want %v", got, at)
	}

	if mediaType != "application/vnd.oci.image.layer.v1.tar+gzip" {
		t.Errorf("the layer forgot how its blob is compressed: %q", mediaType)
	}
}

// TestAnUnreadableNoteIsNoNote: the answer feeds a decision to serve a fragment
// from a blob, so a note that cannot be read has to mean "unpack it the ordinary
// way" rather than a guess about which blob it was.
func TestAnUnreadableNoteIsNoNote(t *testing.T) {
	t.Parallel()

	d := DirStore(t.TempDir())
	layer := ir.NodeID{9}

	d.NoteBlob(layer, ir.NodeID{8}, "")

	// A media type is not optional: a blob whose compression nobody recorded
	// cannot be decompressed, and guessing gzip would fail inside the unpacker
	// with a complaint about a corrupt archive.
	_, _, ok := d.BlobOf(layer)
	if ok {
		t.Error("a note with no media type was read as an answer")
	}
}
