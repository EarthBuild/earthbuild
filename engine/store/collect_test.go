package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// roomForOne is a ceiling that holds one of these layers and not two.
//
// **Derived rather than written down.** The first version said 5000 bytes,
// which holds one 4096-byte layer on APFS and none at all on ext4, where
// `occupies` counts allocated blocks and a layer costs the file's block plus its
// directory's. The tests passed where they were written and removed everything
// on linux (E604).
func roomForOne(t *testing.T, root string, id ir.NodeID) uint64 {
	t.Helper()

	one := SizeAll(LayerStore(root).Path(id))
	if one == 0 {
		t.Fatal("a layer that was just written measures nothing")
	}

	// Half a layer clear of one and well short of two, whatever a block costs
	// here.
	return one + one/2
}

// fill puts a layer of about size bytes into the store and dates its use.
func fill(t *testing.T, root string, n byte, size int, used time.Time) ir.NodeID {
	t.Helper()

	id := ir.NodeID{n}

	dir := LayerStore(root).Path(id)
	err := os.MkdirAll(dir, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(dir, "f"), make([]byte, size), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	index, err := OpenIndex(root)
	if err != nil {
		t.Fatal(err)
	}

	err = index.Note(id)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Chtimes(index.path(id), used, used)
	if err != nil {
		t.Fatal(err)
	}

	return id
}

func TestCollectionTakesTheLeastRecentlyUsedFirst(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	now := time.Now()

	// Same size, so only the dates can decide.
	old := fill(t, root, 1, 4096, now.Add(-72*time.Hour))
	mid := fill(t, root, 2, 4096, now.Add(-2*time.Hour))
	fresh := fill(t, root, 3, 4096, now)

	report, err := Collect(root, roomForOne(t, root, fresh))
	if err != nil {
		t.Fatal(err)
	}

	if report.Removed != 2 || report.Kept != 1 {
		t.Fatalf("removed %d and kept %d, want 2 and 1: %s", report.Removed, report.Kept, report)
	}

	if LayerStore(root).Has(old) || LayerStore(root).Has(mid) {
		t.Error("a layer used longer ago survived one used recently")
	}

	if !LayerStore(root).Has(fresh) {
		t.Error("the most recently used layer was collected")
	}

	// **Forget before deleting**: an index still claiming a collected layer is
	// the state Index exists to prevent.
	index, err := OpenIndex(root)
	if err != nil {
		t.Fatal(err)
	}

	if index.Has(old) || index.Has(mid) {
		t.Error("the index still claims a layer the collector removed")
	}
}

func TestCollectionStopsAtTheCeiling(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	now := time.Now()

	for i := range 5 {
		fill(t, root, byte(i+1), 4096, now.Add(-time.Duration(i)*time.Hour))
	}

	report, err := Collect(root, 20*4096)
	if err != nil {
		t.Fatal(err)
	}

	if report.Removed != 0 {
		t.Errorf("a store already under the ceiling lost %d layers", report.Removed)
	}

	if report.Freed() != 0 {
		t.Errorf("freed %d bytes from a store that fitted", report.Freed())
	}

	// And a ceiling of nothing empties it, which is the other end of the same
	// rule rather than a special case.
	report, err = Collect(root, 0)
	if err != nil {
		t.Fatal(err)
	}

	if report.Kept != 0 {
		t.Errorf("%d layers survived a ceiling of zero", report.Kept)
	}
}

// Reading a layer is what keeps it: a base image filed once and used by every
// build must not look older than last week's throwaway.
func TestReadingALayerKeepsIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	now := time.Now()

	base := fill(t, root, 1, 4096, now.Add(-72*time.Hour))
	junk := fill(t, root, 2, 4096, now.Add(-time.Hour))

	b, err := OpenBlobs(root)
	if err != nil {
		t.Fatal(err)
	}

	if !b.Has(base) {
		t.Fatal("the base layer is not there")
	}

	report, err := Collect(root, roomForOne(t, root, base))
	if err != nil {
		t.Fatal(err)
	}

	if !LayerStore(root).Has(base) {
		t.Errorf("the layer that was just read was collected first: %s", report)
	}

	if LayerStore(root).Has(junk) {
		t.Error("the layer nobody read survived")
	}
}

func TestCollectingNothingIsNotAnError(t *testing.T) {
	t.Parallel()

	_, err := Collect("", 0)
	if err != nil {
		t.Errorf("collecting a store with no root: %v", err)
	}

	report, err := Collect(t.TempDir(), 0)
	if err != nil {
		t.Errorf("collecting an empty store: %v", err)
	}

	if report.Removed != 0 {
		t.Errorf("an empty store yielded %d layers", report.Removed)
	}
}

func TestAReportReadsAsASentence(t *testing.T) {
	t.Parallel()

	got := Report{Before: 3 << 30, After: 1 << 30, Removed: 12, Kept: 4}.String()
	want := "removed 12 layers, freed 2.0 GiB, 4 layers and 1.0 GiB left"

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// **A store too big to measure quickly is exactly the store that needs
// collecting**, so a collector that sizes it with a budget concludes it already
// fits and does nothing. That is what happened: 2.3 GiB reported for a store
// holding 15 GiB, and a prune that removed no layers and said so cheerfully
// (E574).
//
// Not parallel, and the budget is moved rather than the tree grown: a test that
// needs a real 300ms to expire passes on a slow machine and skips on a fast one,
// which reports the machine rather than the code.
func TestASlowStoreIsStillMeasuredWholly(t *testing.T) { //nolint:paralleltest // see the note above
	was := sizeBudget
	sizeBudget = time.Nanosecond

	defer func() { sizeBudget = was }()

	root := t.TempDir()
	now := time.Now()

	fill(t, root, 1, 4096, now.Add(-time.Hour))
	fill(t, root, 2, 4096, now)

	dir := LayerStore(root).Path(ir.NodeID{1})

	if _, complete := Size(dir); complete {
		t.Fatal("the budget did not bite, so this proves nothing")
	}

	if SizeAll(dir) == 0 {
		t.Fatal("the unbudgeted walk measured nothing")
	}

	// Room for one layer. A collector reading floors sees a store of nothing,
	// decides it fits, and removes nothing.
	report, err := Collect(root, 5000)
	if err != nil {
		t.Fatal(err)
	}

	if report.Removed == 0 {
		t.Errorf("a store over the ceiling was left alone because it was slow to"+
			" measure: %s", report)
	}
}
