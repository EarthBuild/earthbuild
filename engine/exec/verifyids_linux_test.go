//go:build linux

package exec_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/nstest"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A layer captured inside a namespace verifies outside it.
//
// Two places compute a layer's identity:
//
//	engine/guest      layer.Take(h.Delta())   inside the guest's namespace
//	engine/exec       LayerStore.Verify       on the host, over the stored tree
//
// `layer.Take` hashes ownership (§3.3, E92), and the two read the same bytes
// through different id mappings: a file the step made as root is uid 0 to the
// guest and the invoking user to the host. So the host recomputes a different
// digest from the one the layer is stored under and **rejects it**.
//
// `Verify` is the integrity check for a layer arriving from *outside* the trust
// domain (§5.3), and its own comment records that nothing calls it yet - *"there
// is no import path, because there is no fleet transport"*. So this is latent
// and fires on S6's first day, when every honest layer from a peer fails the
// check that exists to authenticate it.
//
// **The same disagreement E133 found about observations, one level up and about
// identity itself.** That one cost five of six cache hits; this one would cost
// the fleet.
//
// Testable now, without a transport: capture inside a namespace, verify outside.
// `nstest` re-executes the child, so the parent - which is not mapped - is the
// host half, and the two halves are genuinely on opposite sides of the boundary
// rather than both inside it, which is what E121's fixture got wrong.
func TestALayerCapturedInANamespaceVerifiesOutsideIt(t *testing.T) { //nolint:paralleltest // re-execs
	// Not t.TempDir(): the child's temporary directory is removed when the
	// child exits, and the parent is the half that has to read the tree. A
	// fixed name under the system temp directory outlives the process that
	// made it, and the parent removes it.
	root := filepath.Join(os.TempDir(), "earth-verify-tree")
	note := filepath.Join(os.TempDir(), "earth-verify-probe")

	// The child captures; it is the one with a mapping.
	if nstest.In(t) {
		err := os.MkdirAll(root, 0o755) //nolint:gosec // matches a step's own mode
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(filepath.Join(root, "f.txt"), []byte("payload\n"), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		// What the guest does when it captures a step's delta, through the
		// same function - not a re-implementation of it.
		uids, gids := guest.OwnIDMaps()

		c, err := layer.TakeIn(root, uids, gids)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(note, []byte(c.ID.String()), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		return
	}

	// The parent, unmapped, is the host half.
	t.Cleanup(func() { _ = os.RemoveAll(root); _ = os.Remove(note) })

	b, err := os.ReadFile(note)
	if err != nil {
		t.Skipf("the child did not run here: %v", err)
	}

	captured := strings.TrimSpace(string(b))

	outside, err := layer.Take(root)
	if err != nil {
		t.Fatalf("the host cannot read the tree the child captured: %v", err)
	}

	// Compared as text, because that is what crossed between the two processes
	// and parsing it back would test the parser rather than the digest.
	if outside.ID.String() != captured {
		t.Errorf("a layer captured inside a namespace does not verify outside it:"+
			"\n  captured %s\n  verified %s"+
			"\n  LayerStore.Verify recomputes this digest to authenticate a layer from"+
			"\n  outside the trust domain, so every honest layer would be rejected",
			captured, outside.ID)
	}

	_ = store.LayerStore(root)
}
