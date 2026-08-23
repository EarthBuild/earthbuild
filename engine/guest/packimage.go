package guest

import (
	"fmt"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// packImageInto writes a loadable image archive into this guest's store.
//
// The layers arrive as ids and are resolved here, because the host and the
// guest see the store at different paths and a path from the wrong side names
// nothing (E558). Everything else about the image - its name, its
// configuration, the platform it is for - is the build's and is sent.
//
// Filed under the packing step's own identity, so two loads of different images
// in one build do not land on each other and a repeat of the same one is the
// same file. The same rule the host used when it did this, because it is the
// rule the step that loads the archive relies on.
func packImageInto(root string, into ir.NodeID, layers []ir.NodeID, spec image.Spec) error {
	held := store.LayerStore(root)

	spec.Layers = make([]image.LayerSource, 0, len(layers))

	for _, id := range layers {
		// Refused rather than skipped. A missing layer here would produce an
		// image that loads and is missing files, which the daemon reports as a
		// program that is not there - a message with nothing in it to connect
		// to the build that lost the layer.
		if !held.Has(id) {
			return fmt.Errorf("pack %s: this store holds no layer %s", spec.Ref, id)
		}

		spec.Layers = append(spec.Layers, image.FromDir(held.Path(id)))
	}

	return image.WriteArchive(filepath.Join(root, "images", into.String()), spec)
}
