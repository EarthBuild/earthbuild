package overlay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/nstest"
)

// A deep stack mounts, wherever the store happens to live.
//
// Overlayfs reads one page of mount options - 4095 bytes - and every byte of
// every lowerdir path is charged against it. The symlink farm already shortens
// the *names* to 12 characters; what it cannot shorten is the path to the farm,
// so the number of layers that fit depends on how deep the store is. A store
// under `~/.cache` fits about eighty; one under a long temporary directory
// fails at thirty-seven, which is what the engine's own build hit:
//
//	a stack of 37 layers needs 4112 bytes of mount options and the kernel reads 4095
//
// That is a real limit reached for an accidental reason. This repository's own
// `+lint` needs 42 layers, so **the engine could not build its own source** on a
// machine whose store path was long enough - and three tests said so only once
// `EARTH_TEST_BUILD` and `EARTH_GUESTD` were both set (E163).
//
// The store here is deliberately deep, so the test measures the property rather
// than the tester's luck with `t.TempDir`.
func TestADeepStackMountsFromADeepStore(t *testing.T) {
	if !nstest.In(t) {
		return
	}

	// Padded to roughly what a real temporary store costs, and then some.
	deep := filepath.Join(t.TempDir(),
		strings.Repeat("a", 40), strings.Repeat("b", 40), "store")

	err := os.MkdirAll(deep, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	m, err := New(deep)
	if err != nil {
		t.Skipf("no overlay materialiser here: %v", err)
	}

	const depth = 60

	var stack []ir.NodeID

	for i := range depth {
		id := ir.NodeID{byte(i + 1), byte(i / 256)}

		err := m.WriteLayer(id, map[string]string{
			"f" + string(rune('a'+i%26)): "x",
		})
		if err != nil {
			t.Fatal(err)
		}

		stack = append(stack, id)
	}

	h, err := m.Materialise(t.Context(), stack)
	if err != nil {
		t.Fatalf("a stack of %d layers under a %d-character store did not mount: %v",
			depth, len(deep), err)
	}

	t.Cleanup(func() { _ = h.Release() })

	// And it is the whole stack, not a truncated one: the kernel's answer to an
	// over-long option string is to read part of it, so a mount that succeeded
	// is not by itself evidence that every layer arrived.
	for i := range depth {
		at := filepath.Join(h.Root(), "f"+string(rune('a'+i%26)))
		if _, err := os.Lstat(at); err != nil {
			t.Fatalf("layer %d is not in the merged view: %v", i, err)
		}
	}
}
