package cache_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cache"
	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// An image is many layers, and with the store on the guest's device that is how
// it arrives: `Result.Layers`, oldest first, with no single delta to name. The
// entry that records it had only the singular `Layer`, so every such result was
// dropped by the zero-layer guard and `FROM` missed on every build for ever -
// which then booted a sandbox to re-materialise an image already held (E872).
func TestAnImageStackSurvivesTheCache(t *testing.T) {
	t.Parallel()

	c, err := cache.Open(t.TempDir())
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}

	key := ir.NodeID{1}
	want := []ir.NodeID{{2}, {3}, {4}}

	c.Put(key, core.Entry{Layers: want, Declared: true, Writer: "w"})

	got, ok := c.Get(key)
	if !ok {
		t.Fatal("an entry naming a stack was not stored: FROM can never hit")
	}

	if len(got.Layers) != len(want) {
		t.Fatalf("Layers = %v, want %v", got.Layers, want)
	}

	for i := range want {
		if got.Layers[i] != want[i] {
			t.Errorf("Layers[%d] = %v, want %v", i, got.Layers[i], want[i])
		}
	}
}

// The order is the stack, not a set: layers are pushed oldest first and a
// reordering silently builds a different filesystem.
func TestAnImageStackKeepsItsOrder(t *testing.T) {
	t.Parallel()

	c, err := cache.Open(t.TempDir())
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}

	key := ir.NodeID{9}
	want := []ir.NodeID{{3}, {1}, {2}}

	c.Put(key, core.Entry{Layers: want, Declared: true, Writer: "w"})

	got, _ := c.Get(key)
	for i := range want {
		if i < len(got.Layers) && got.Layers[i] != want[i] {
			t.Errorf("Layers[%d] = %v, want %v (order is the stack)", i, got.Layers[i], want[i])
		}
	}
}

// A claim naming nothing at all is still refused: that is the guard the stack
// case has to pass through without weakening it (green paper I11).
func TestAnEntryNamingNothingIsStillRefused(t *testing.T) {
	t.Parallel()

	c, err := cache.Open(t.TempDir())
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}

	key := ir.NodeID{7}
	c.Put(key, core.Entry{Declared: true, Writer: "w"})

	if _, ok := c.Get(key); ok {
		t.Error("an entry with neither a layer nor a stack was stored")
	}
}
