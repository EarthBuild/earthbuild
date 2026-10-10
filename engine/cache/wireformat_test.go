package cache_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cache"
	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The entry file outlives the process that wrote it and is read by whatever
// binary comes next, including an older one. `layers` was added after the format
// existed (E872), so two directions have to hold: an entry without a stack must
// not grow the field, and an entry with one must be ignorable by a reader that
// has never heard of it rather than believed in part.
func TestTheEntryFileSaysWhatItMeans(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	c, err := cache.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	c.Put(ir.NodeID{1}, core.Entry{Layer: ir.NodeID{9}, Declared: true, Writer: "w"})
	c.Put(ir.NodeID{2}, core.Entry{Layers: []ir.NodeID{{3}, {4}}, Declared: true, Writer: "w"})

	read := func(k ir.NodeID) map[string]any {
		t.Helper()

		b, err := os.ReadFile(filepath.Join(dir, "actions", hex.EncodeToString(k[:])+".json"))
		if err != nil {
			t.Fatalf("read entry: %v", err)
		}

		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		return m
	}

	// A single-layer entry is byte-for-byte what it always was. An older reader
	// must find nothing new in it.
	if _, ok := read(ir.NodeID{1})["layers"]; ok {
		t.Error("an entry with one layer grew a layers field, which every older reader will now see")
	}

	// And a stack entry names a zero layer, which is exactly what an older
	// reader rejects. That is the intended outcome: a miss, not a hit on a
	// filesystem it cannot assemble.
	stack := read(ir.NodeID{2})

	got, ok := stack["layers"].([]any)
	if !ok || len(got) != 2 {
		t.Fatalf("layers = %v, want two entries", stack["layers"])
	}

	if stack["layer"] != "0000000000000000000000000000000000000000000000000000000000000000" {
		t.Errorf("layer = %v, want the zero digest an older reader refuses", stack["layer"])
	}
}
