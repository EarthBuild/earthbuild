package guest

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// PackLayer writes one layer of this guest's store as an OCI blob.
//
// **The first thing the host asks the guest to hand it rather than to file.**
// Everything else the guest produces is left in the store for the host to pick
// up, which works because both see one directory and stops working the moment
// the store is a disk the guest owns (E553). `SAVE IMAGE` reads every layer of
// a stack to build an OCI layout, and it is one of the two host readers left.
//
// Bytes and nothing else. The blob's name is the digest of what is written, so
// the caller hashes the stream as it copies and needs no answer back - which is
// what makes this expressible as a pipe rather than as a protocol.
//
// The same `image.Pack` the host runs today, on the same directory, so the blob
// is the one the host would have produced. That is the property the transport
// has to have and the one a test can check without a machine.
func PackLayer(root string, id ir.NodeID, w io.Writer) error {
	at := filepath.Join(root, "layers", id.String())

	fi, err := os.Stat(at)
	if err != nil {
		return fmt.Errorf("pack layer %s: %w", id, err)
	}

	if !fi.IsDir() {
		return fmt.Errorf("pack layer %s: %s is not a layer directory", id, at)
	}

	_, _, err = image.Pack(at, w)
	if err != nil {
		return fmt.Errorf("pack layer %s: %w", id, err)
	}

	return nil
}
