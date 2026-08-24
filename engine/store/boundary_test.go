package store

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// knowsTheLayout is every file that builds a path inside the layer store, and
// which side of the sandbox boundary is entitled to.
//
// **The disk's work list, kept by a test rather than by a plan.** Putting the
// store on a block device the guest owns does not change what a layer is; it
// changes who can open one. Every entry below that says `host` is a place that
// reads or writes the store through the host's filesystem and will have to ask
// the guest instead, and the point of the register is that a new one cannot
// appear without somebody deciding which it is (E553).
//
// The categories, and what each means for the disk:
//
//	store   the store's own implementation. Runs wherever the store is, so it
//	        moves with it and needs no decision.
//	guest   inside the sandbox already. Becomes the only kind once the disk
//	        exists, and is the shape the others have to take.
//	host    reads or writes the store from outside. **This is the work.**
//	setup   creates the directory before anything is in it, which a disk does
//	        by existing.
const (
	sideStore = "store"
	sideGuest = "guest"
	sideHost  = "host"
	sideSetup = "setup"
	// sideIndex reads the store's *index*, which is the host's half of the
	// disk rather than a problem with it: the index exists precisely so a host
	// that cannot open the store can still answer what it holds (E542).
	sideIndex = "index"
)

var knowsTheLayout = map[string]string{
	// The store itself.
	"engine/store/store.go":         sideStore,
	"engine/store/layerstore.go":    sideStore,
	"engine/store/view.go":          sideStore,
	"engine/store/squash.go":        sideStore,
	"engine/store/placecaptured.go": sideStore,
	"engine/store/declaration.go":   sideStore,
	"engine/store/index.go":         sideStore,
	// The collector reads the layer directory to size and remove what is in it.
	// Store-side by necessity rather than by choice: once the store is a device,
	// collecting is something only whoever mounts it can do, and a host-side
	// collector would be a reader found on the day that changes (E574).
	"engine/store/collect.go": sideStore,

	// Inside the sandbox, which is where all of this ends up.
	"engine/guest/guest.go": sideGuest,
	// Packs one layer onto a pipe for a host that cannot open the store. It is
	// the answer to a `host` entry rather than a new problem: the reading moved
	// inside, which is the shape every remaining one has to take (E556).
	"engine/guest/packlayer.go": sideGuest,
	// Builds a loadable image archive from layers it holds, which the host used
	// to build and leave where the guest would find it (E558).
	"engine/guest/packimage.go":           sideGuest,
	"engine/mat/overlay/overlay_linux.go": sideGuest,

	// The work. Each of these opens the store from the host.
	//
	// `cli/images.go` reads layers to write an OCI image out; it becomes an
	// export the guest performs, which is the shape `Export` already has.
	//
	// `fleet/layers.go` serves layers to peers and receives them. A worker's
	// store is its own, so this is the same question one level up: either the
	// fleet talks to the guest, or a worker's store stays a directory and only
	// a developer's is a disk.
	"engine/cli/images.go":     sideHost,
	"engine/fleet/layers.go":   sideHost,
	"engine/exec/exec.go":      sideHost,
	"engine/exec/packimage.go": sideHost,
	// `exec/squash.go` asks a guest that is already running and flattens here
	// only when there is none - which is every backend without a machine, and
	// their store is local anyway (E557).
	"engine/exec/squash.go": sideHost,

	// The host asking the index what the store holds, which is the arrangement
	// the disk is for rather than an obstacle to it.
	"engine/cli/cli.go":        sideIndex,
	"engine/cli/conditions.go": sideIndex,

	// `decl/store.go` was host too, and is not any more. The host read the
	// `.decl` files beside a base's layers to learn what the image declared;
	// the guest had already read them to build the mount, so the answer now
	// travels back with the handle and there is one reader instead of two
	// (E554). The remaining caller is the materialiser, inside the sandbox.
	"engine/decl/store.go": sideGuest,

	// Making the directory, which a disk does by being attached.
	//
	// `tools/fleetprobe` makes one for a measurement it then throws away. It is
	// a tool rather than the engine, and it was the file this register found on
	// its first run - which is the argument for the register: a grep over
	// `engine/` and `cmd/` had already been read and had already missed it.
	"engine/exec/apple_darwin.go": sideSetup,
	"engine/exec/native_linux.go": sideSetup,
	"tools/fleetprobe/main.go":    sideSetup,
}

// Every file that knows the store's layout is registered, on one side or the
// other.
//
// A test rather than a document because the list is the plan: an unregistered
// file is a host-side reader nobody decided about, and the way the disk fails
// is not with a design that cannot work - it is with a reader that was missed,
// found on the day the store stops being a directory.
func TestEveryFileThatKnowsTheStoreLayoutIsRegistered(t *testing.T) {
	t.Parallel()

	found := map[string]bool{}

	err := filepath.WalkDir("../..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "build", "testdata", "examples":
				return filepath.SkipDir
			}

			return nil
		}

		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}

		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}

		for line := range strings.SplitSeq(string(b), "\n") {
			// Two ways to reach the store, and the second was added because the
			// first was evaded by an ordinary refactor: `cli/images.go` stopped
			// joining `"layers"` when it started calling `LayerStore.Path`, and
			// the register declared it cured. It was not - it reads the same
			// directories through a helper.
			//
			// *A detector that names a spelling is one refactor from being
			// decorative* (E545 said it about a different guard, and this is
			// the same guard's turn). So this asks who *opens* the store, which
			// is the question the disk actually poses.
			//
			// The path form still counts: `Layers []descriptor json:"layers"`
			// is an OCI manifest field and knows nothing about this engine's
			// directories, which is why the word alone is not enough.
			builds := strings.Contains(line, `"layers"`) && strings.Contains(line, "Join(")
			opens := strings.Contains(line, "store.LayerStore(") ||
				strings.Contains(line, "store.DirStore(") ||
				strings.Contains(line, "store.OpenBlobs(") ||
				strings.Contains(line, "store.OpenIndex(")

			if builds || opens {
				rel := filepath.ToSlash(strings.TrimPrefix(filepath.Clean(p), "../../"))
				found[rel] = true

				break
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(found) == 0 {
		t.Fatal("no file appears to build a path inside the layer store, so this" +
			" test is checking nothing - the match has probably rotted")
	}

	for p := range found {
		side, ok := knowsTheLayout[p]
		if !ok {
			t.Errorf("%s builds a path inside the layer store and is not registered."+
				"\n  Add it to knowsTheLayout as store, guest, host or setup."+
				"\n  A host-side reader nobody decided about is how the disk fails:"+
				"\n  not with a design that cannot work, but with a reader found on"+
				"\n  the day the store stops being a directory.", p)

			continue
		}

		switch side {
		case sideStore, sideGuest, sideHost, sideSetup, sideIndex:
		default:
			t.Errorf("%s is registered as %q, which is not one of store, guest, host, setup, index", p, side)
		}
	}

	// The register may not outlive what it registers: an entry for a file that
	// no longer names the store is a work item somebody has already done and
	// nobody has crossed off, and a list with stale entries stops being read.
	for p := range knowsTheLayout {
		if !found[p] {
			t.Errorf("%s is registered as knowing the store's layout and no longer does."+
				"\n  Remove it: a list with entries nobody can act on stops being read.", p)
		}
	}
}
