package guest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/decl"
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

	// **The exit.** A layer holding a credential has gone nowhere while it sits
	// in the store; packing it into an image is what sends it somewhere else.
	// Read from beside the layer rather than remembered, so a build that took it
	// from the cache is told what the build that made it found (E694).
	err := refusePackingLeaked(root, layers)
	if err != nil {
		return err
	}

	spec.Layers = make([]image.LayerSource, 0, len(layers))

	for _, id := range layers {
		// **A declaration is not a layer, and its absence is not a loss.**
		// An image's environment travels as a stack element so that a worker
		// fetching every id in the stack fetches it too (green paper §3.2a),
		// but it is stored as `layers/<id>.decl` - a file, where the test
		// below wants a tree. Asked of it, that test could only ever fail, and
		// a correct `WITH DOCKER --load` was refused for losing a layer that
		// was never one (E749). What it declares reaches the image through
		// spec.Config, which the host filled in from the same declaration.
		if decl.Has(root, id) {
			continue
		}

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

// EnvAllowLeakedSecrets lets an image be packed from a layer holding a secret.
//
// The check is on by default and this is the way out, for somebody who bakes a
// credential in on purpose - an `.npmrc`, a `.netrc`. Read here as well as on
// the host because the refusal happens on whichever side is doing the packing.
const EnvAllowLeakedSecrets = "EARTH_ALLOW_LEAKED_SECRETS"

// refusePackingLeaked stops an image being built out of a layer that holds a
// credential.
//
// The message names the secret and where it was found and never the value: it
// goes to the build's output, which is the log the credential was being kept out
// of.
func refusePackingLeaked(root string, layers []ir.NodeID) error {
	if os.Getenv(EnvAllowLeakedSecrets) != "" {
		return nil
	}

	st := store.DirStore(root)

	var found []string

	for _, id := range layers {
		found = append(found, st.LeakedIn(id)...)
	}

	if len(found) == 0 {
		return nil
	}

	sort.Strings(found)

	return fmt.Errorf("a secret is in a layer of this image, and the image is"+
		" not packed"+
		"\n  %s"+
		"\n  an image is packed to be used elsewhere, which is where the"+
		" credential would go"+
		"\n  keep the secret out of the layer, or set %s if it belongs there",
		strings.Join(found, "\n  "), EnvAllowLeakedSecrets)
}
