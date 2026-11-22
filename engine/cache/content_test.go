package cache_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Two runs of a deterministic step are not a disagreement.
//
// A layer's identity includes its timestamps (I8), and creating a directory
// stamps it with the wall clock - so two runs of one step produce two layer
// digests while producing the same bytes. Measured, not assumed: building
//
//	RUN mkdir -p /out/dir && echo fixed > /out/dir/a.txt
//
// twice from a cold store gives layers `d599575a…` and `679c4e36…`, with the
// base image identical both times.
//
// The conflict detection added in E76 compared `Layer`, so **every re-run after
// eviction of a step that creates a directory would have been reported as a
// key claiming two results** - which is most steps, and would have made the
// warning fire on healthy builds until nobody read it. The one diagnostic this
// engine has for non-determinism, trained away by its own false positives.
//
// `core.Result.Content` is the digest with timestamps excluded and exists for
// exactly this comparison. The guest computes it, the protocol carries it, the
// executor returns it, and until now nothing read it: four layers of plumbing
// to a dead end (E81).
func TestSameContentUnderDifferentLayersIsNotAConflict(t *testing.T) {
	t.Parallel()

	c := openCache(t)
	k := key(0x71)

	content := ir.NodeID{0xcc}

	c.Put(k, core.Entry{Layer: ir.NodeID{0xa1}, Content: content, Bytes: 1, Writer: testFirst})
	c.Put(k, core.Entry{Layer: ir.NodeID{0xb2}, Content: content, Bytes: 1, Writer: testSecond})

	if n := c.ConflictCount(); n != 0 {
		t.Errorf("two runs of a deterministic step were reported as %d conflict(s): %+v",
			n, c.Conflicts())
	}

	// And the first claim still stands: refusing the rewrite is I9, and it does
	// not stop being I9 because the rewrite was harmless.
	if got, ok := c.Get(k); !ok || got.Layer != (ir.NodeID{0xa1}) {
		t.Errorf("the held entry changed: %+v %v", got, ok)
	}
}

// Different content under one key is still a disagreement.
//
// The arm that keeps the fix from being a way of never reporting anything.
func TestDifferentContentIsStillAConflict(t *testing.T) {
	t.Parallel()

	c := openCache(t)
	k := key(0x72)

	c.Put(k, core.Entry{Layer: ir.NodeID{0xa1}, Content: ir.NodeID{0xc1}, Writer: testFirst})
	c.Put(k, core.Entry{Layer: ir.NodeID{0xb2}, Content: ir.NodeID{0xc2}, Writer: testSecond})

	if n := c.ConflictCount(); n != 1 {
		t.Fatalf("a step that produced different bytes was recorded as %d conflict(s)", n)
	}
}

// An entry with no content falls back to comparing layers.
//
// Entries written before this field existed have none, and so does any producer
// that does not compute one - a host step, or an executor that captured
// nothing. Comparing an absent content against a present one would read every
// such pair as agreement, which is the direction that loses a real finding.
//
// Falling back to `Layer` restores the old behaviour for those, false positives
// included. That is the right trade: the old behaviour is over-reporting, and
// over-reporting on entries from a previous version is a smaller fault than
// silently declaring them equal.
func TestAnEntryWithoutContentComparesLayers(t *testing.T) {
	t.Parallel()

	c := openCache(t)
	k := key(0x73)

	c.Put(k, core.Entry{Layer: ir.NodeID{0xa1}, Writer: "old"})
	c.Put(k, core.Entry{Layer: ir.NodeID{0xb2}, Content: ir.NodeID{0xcc}, Writer: "new"})

	if n := c.ConflictCount(); n != 1 {
		t.Errorf("a contentless entry was compared as though it agreed: %d conflict(s)", n)
	}
}

// Content survives the round trip to disk.
//
// It is stored, not merely held: the comparison happens against what a previous
// *build* wrote, so a field that lived only in memory would make every
// cross-process comparison fall back to layers - which is the case the fix is
// for.
func TestContentSurvivesTheProcess(t *testing.T) {
	t.Parallel()

	c := openCache(t)
	k := key(0x74)

	want := ir.NodeID{0xcc}
	c.Put(k, core.Entry{Layer: ir.NodeID{0xa1}, Content: want, Bytes: 3, Writer: testFirst})

	got, ok := c.Get(k)
	if !ok {
		t.Fatal("the entry was not stored")
	}

	if got.Content != want {
		t.Errorf("the content digest did not survive: %s", got.Content)
	}
}
