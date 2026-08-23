package guest_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The guest packs the blob the host would have packed.
//
// This is the whole property the transport has to have. `SAVE IMAGE` builds an
// OCI layout by reading every layer of a stack from the host's filesystem, and
// once the store is a disk the guest owns it cannot - so the guest hands the
// bytes over instead (E553, E556). If those bytes differed in any way the image
// would differ, and an image that changes because of *where it was assembled*
// is the failure this engine spends its invariants on.
//
// Byte-for-byte rather than "both are valid tars": a blob is named by the
// digest of its contents, so equal-but-different is a different image.
func TestTheGuestPacksTheBlobTheHostWouldHave(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := ir.NodeID{7}

	at := filepath.Join(root, "layers", id.String())

	err := os.MkdirAll(filepath.Join(at, "usr", "bin"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(at, "usr", "bin", "tool"), []byte("elf"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink("/usr/bin/tool", filepath.Join(at, "usr", "bin", "link"))
	if err != nil {
		t.Fatal(err)
	}

	// What the host does today, from the directory it can see.
	var host bytes.Buffer

	_, _, err = image.Pack(at, &host)
	if err != nil {
		t.Fatal(err)
	}

	// What the guest hands over, from the store it owns.
	var fromGuest bytes.Buffer

	err = guest.PackLayer(root, id, &fromGuest)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(host.Bytes(), fromGuest.Bytes()) {
		t.Errorf("the guest packed %d bytes and the host packed %d, and they differ"+
			"\n  a blob is named by the digest of its contents, so an image"+
			"\n  assembled through the guest would not be the image assembled"+
			"\n  on the host - and where it was assembled is not allowed to show",
			fromGuest.Len(), host.Len())
	}
}

// A layer that is not there is refused, by name.
//
// The caller is a pipe: an empty stdout and a zero exit would be an empty blob
// filed under the digest of nothing, which is an image with a layer missing and
// no error anywhere.
func TestPackingALayerThatIsNotThereSaysSo(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	id := ir.NodeID{9}

	err := guest.PackLayer(t.TempDir(), id, &out)
	if err == nil {
		t.Fatal("packing a layer the store does not hold reported success")
	}

	if out.Len() != 0 {
		t.Errorf("a failed pack wrote %d bytes, which a caller streaming to a"+
			" blob file would have kept", out.Len())
	}
}
