package decl

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// suffix names a declaration in the layer store.
//
// **Beside the layer, never inside it.** A layer is named by its content, so a
// file added to the tree is a layer that is no longer what it says it is - and
// it would appear in the step's filesystem, which is the one thing a declaration
// must never do. The suffix cannot collide with a layer directory, whose name is
// a hex digest.
const suffix = ".decl"

// Path is where a declaration lives in a store.
//
// One definition, imported by everything that reaches into the layer store, so
// the fleet and the materialiser cannot drift about where a declaration is.
func Path(store string, id ir.NodeID) string {
	return filepath.Join(store, "layers", id.String()+suffix)
}

// Has reports whether the store holds this declaration.
//
// Distinct from a layer's presence on purpose: a stack element is one or the
// other, and a materialiser that could not tell them apart would have to guess
// what an absent element meant (I18).
func Has(store string, id ir.NodeID) bool {
	fi, err := os.Stat(Path(store, id))

	return err == nil && fi.Mode().IsRegular()
}

// Write files a declaration under its own identity, returning it.
//
// **The name is not the caller's to choose.** A declaration is content-addressed
// like everything else here, so it is named by what is in it - which is what
// makes two machines that assemble the same declaration agree without asking.
func Write(store string, d Declaration) (ir.NodeID, error) {
	id := ID(d)
	at := Path(store, id)

	// 0750, matching the layer store this sits in: the store is the engine's and
	// the guest reads it as root, so nothing else needs to see it.
	err := os.MkdirAll(filepath.Dir(at), 0o750)
	if err != nil {
		return ir.NodeID{}, fmt.Errorf("prepare the layer store: %w", err)
	}

	// Staged and renamed, so a reader never sees half a declaration: the name is
	// a promise about the contents, and a partial file under it would be
	// believed.
	tmp, err := os.CreateTemp(filepath.Dir(at), "."+id.String()+"-")
	if err != nil {
		return ir.NodeID{}, fmt.Errorf("stage a declaration: %w", err)
	}

	_, err = tmp.Write(Encode(d))
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())

		return ir.NodeID{}, fmt.Errorf("write a declaration: %w", err)
	}

	err = tmp.Close()
	if err != nil {
		_ = os.Remove(tmp.Name())

		return ir.NodeID{}, fmt.Errorf("write a declaration: %w", err)
	}

	err = os.Rename(tmp.Name(), at)
	if err != nil {
		_ = os.Remove(tmp.Name())

		return ir.NodeID{}, fmt.Errorf("file a declaration: %w", err)
	}

	return id, nil
}

// Read returns the declaration, whether the store held it, and any error.
//
// **Absent and damaged are different answers.** Not there means fetch it, which
// is the right move whatever the reason. There and unreadable means something is
// wrong with this store, and reporting it as a miss would silently drop what an
// image declares - the failure the whole mechanism exists to prevent.
func Read(store string, id ir.NodeID) (Declaration, bool, error) {
	b, err := os.ReadFile(Path(store, id))
	if errors.Is(err, fs.ErrNotExist) {
		return Declaration{}, false, nil
	}

	if err != nil {
		return Declaration{}, false, fmt.Errorf("read declaration %v: %w", id, err)
	}

	d, err := Decode(b)
	if err != nil {
		// **The remedy is simple and easy to miss.** A declaration is named by
		// its contents, so nothing here is irreplaceable: whatever wrote it can
		// write it again, and a reader who is not told that is left wondering
		// what they have lost.
		return Declaration{}, false, fmt.Errorf("the declaration at %s is damaged: %w"+
			"\n  it is named by its contents, so it is safe to delete and will be fetched again",
			Path(store, id), err)
	}

	return d, true, nil
}
