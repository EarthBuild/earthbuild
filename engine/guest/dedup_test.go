//go:build linux

package guest_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// Two different steps that produce identical output store one layer, not two.
//
// A layer is named by ℋ over its content (green paper §3.3a), so identity is a
// consequence of what it holds rather than of which step made it. Two commands
// arriving at the same bytes converge on the same directory; the second commit
// finds it already there and does nothing.
func TestIdenticalOutputsAreStoredOnce(t *testing.T) {
	t.Parallel()

	store := t.TempDir()

	stamp := time.Unix(1700000000, 42)

	// Two "steps", each writing the same file.
	for _, name := range []string{"step-a", "step-b"} {
		delta := filepath.Join(t.TempDir(), name)
		err := os.MkdirAll(delta, 0o750)
		if err != nil {
			t.Fatal(err)
		}

		p := filepath.Join(delta, "out")
		err = os.WriteFile(p, []byte("identical"), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		err = os.Chtimes(p, stamp, stamp)
		if err != nil {
			t.Fatal(err)
		}

		err = os.Chtimes(delta, stamp, stamp)
		if err != nil {
			t.Fatal(err)
		}

		c, err := layer.Take(delta)
		if err != nil {
			t.Fatal(err)
		}

		err = commitFor(t, store, delta, c.ID)
		if err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(filepath.Join(store, "layers"))
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		t.Errorf("two identical outputs produced %d stored layers, want 1", len(entries))
	}
}

// The limit of that deduplication, stated rather than discovered later: content
// that differs *only* in mtime is a different layer.
//
// ℓ_id includes timestamps because a layer must restore faithfully (I8), so two
// builds producing byte-identical files at different moments store both. ℓ_con -
// the timestamp-free digest - is what would detect it, and it is currently used
// for determinism screening rather than for storage.
//
// **[GAP]** deduplicating on ℓ_con would need a second index and a rule for
// which timestamps win. Not built, and named here so the cost is visible.
func TestTimestampsDefeatDeduplication(t *testing.T) {
	t.Parallel()

	ids := make([]ir.NodeID, 2)

	for i, ns := range []int{1, 2} {
		delta := t.TempDir()

		p := filepath.Join(delta, "out")
		err := os.WriteFile(p, []byte("identical"), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		stamp := time.Unix(1700000000, int64(ns))
		err = os.Chtimes(p, stamp, stamp)
		if err != nil {
			t.Fatal(err)
		}

		c, err := layer.Take(delta)
		if err != nil {
			t.Fatal(err)
		}

		ids[i] = c.ID
	}

	if ids[0] == ids[1] {
		t.Skip("this filesystem does not record nanoseconds, so the case cannot arise")
	}

	t.Logf("byte-identical content, mtimes 1ns apart, stored as two layers:\n  %s\n  %s", ids[0], ids[1])
}

func commitFor(t *testing.T, store, delta string, id ir.NodeID) error {
	t.Helper()

	return guest.ExportCommit(context.Background(), store, delta, id)
}
