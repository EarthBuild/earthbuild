package store

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// BlobSuffix names the note recording which compressed blob a layer came from.
//
// Beside the layer, as `.unmarked` and the configuration are: it is a fact about
// that layer and it should go away when the layer does.
const BlobSuffix = ".blob"

// NoteBlob records that a layer unpacks from a stored blob.
//
// **The join a lazy pull turns on.** A blob is named by the hash of its
// compressed bytes and a layer by the hash of the tree it unpacks to, and
// nothing relates the two except the pull that saw both. Without the note a
// store holding a perfectly good 61MB blob cannot tell which of its layers it
// is, and unpacks the layer again from the network.
//
// Best effort, like `noteUnmarked`: a note that cannot be written costs the
// ordinary unpack, which is what happened before it existed.
func (d DirStore) NoteBlob(id, at ir.NodeID, mediaType string) {
	if mediaType == "" {
		return
	}

	at2 := d.LayerPath(id)

	// The store may be cold: a note can be the first thing written to it, and a
	// missing directory is not a reason to lose the join.
	err := os.MkdirAll(filepath.Dir(at2), 0o750)
	if err != nil {
		return
	}

	// One line, two fields: the blob's name and how to decompress it. A blob
	// whose compression nobody recorded cannot be read, and guessing gzip fails
	// inside the unpacker with a complaint about a corrupt archive - the wrong
	// component entirely.
	_ = os.WriteFile(at2+BlobSuffix,
		[]byte(at.String()+" "+mediaType+"\n"), 0o600)
}

// BlobOf is the blob a layer came from, if this store saw it arrive.
//
// Absent, unreadable and malformed are one answer - not found - because the
// decision it feeds is "serve a fragment from the blob or unpack the layer the
// ordinary way", and the ordinary way is always available. A guess here would
// serve one layer's files as another's.
func (d DirStore) BlobOf(id ir.NodeID) (at ir.NodeID, mediaType string, ok bool) {
	b, err := os.ReadFile(d.LayerPath(id) + BlobSuffix)
	if err != nil {
		return ir.NodeID{}, "", false
	}

	name, mediaType, found := strings.Cut(strings.TrimSpace(string(b)), " ")
	if !found || mediaType == "" {
		return ir.NodeID{}, "", false
	}

	at, err = ir.ParseNodeID(name)
	if err != nil {
		return ir.NodeID{}, "", false
	}

	return at, mediaType, true
}
