package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// openIndex is OpenIndex, failing the test rather than returning.
func openIndex(t *testing.T, root string) Index {
	t.Helper()

	i, err := OpenIndex(root)
	if err != nil {
		t.Fatal(err)
	}

	return i
}

// disagreements is Index.Disagrees, failing the test rather than returning.
func disagreements(t *testing.T, root string) (missing, claimed []ir.NodeID) {
	t.Helper()

	missing, claimed, err := openIndex(t, root).Disagrees()
	if err != nil {
		t.Fatal(err)
	}

	return missing, claimed
}

// Every way a layer enters the store records it.
//
// The risk the index carries is not the lookup, it is completeness: a path that
// files a layer and does not record it costs a rebuild, silently, on a machine
// that has the layer. Publishing is the one seam every such path goes through,
// so this asserts the seam holds for each of them.
func TestEveryWayALayerIsFiledRecordsIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// Published directly, as a peer transfer and a step's capture both do.
	direct := ir.NodeID{1}

	err := Publish(root, direct, staged(t, root, ".incoming-direct", "a"))
	if err != nil {
		t.Fatal(err)
	}

	// Named, as a build context is.
	named := ir.NodeID{2}

	err = DirStore(root).PutNamed(named, staged(t, root, ".incoming-named", "b"))
	if err != nil {
		t.Fatal(err)
	}

	// Squashed, as a stack too deep to mount is.
	into := ir.NodeID{3}

	err = DirStore(root).Squash(context.Background(), into, []ir.NodeID{direct, named})
	if err != nil {
		t.Fatal(err)
	}

	missing, claimed := disagreements(t, root)

	if len(missing) != 0 {
		t.Errorf("the store holds layers the index does not record: %v"+
			"\n  a machine with the layer would rebuild it, and say nothing", missing)
	}

	if len(claimed) != 0 {
		t.Errorf("the index records layers the store does not hold: %v"+
			"\n  which is a cache hit against a layer that is not there", claimed)
	}
}

// A store filled before the index existed is not a store with nothing in it.
//
// *Absent is not empty.* Every machine that has ever run this engine has a store
// full of layers and no index, and an index that answered "no" about all of them
// would throw away a cache nobody could see was gone - a first build after an
// upgrade that rebuilds everything and reports success. So opening an index that
// is not there builds it from the store, once.
func TestAStoreFilledBeforeTheIndexExistedKeepsItsLayers(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	for i, name := range []string{".s1", ".s2", ".s3"} {
		id := ir.NodeID{byte(i + 1)}

		err := Publish(root, id, staged(t, root, name, name))
		if err != nil {
			t.Fatal(err)
		}
	}

	// The state of every existing store: layers, and no record of them.
	err := os.RemoveAll(filepath.Join(root, "index"))
	if err != nil {
		t.Fatal(err)
	}

	if !openIndex(t, root).Has(ir.NodeID{1}) {
		t.Fatal("a store filled before the index existed reported holding none" +
			" of its layers:\n  every machine upgrading to this would rebuild its" +
			" whole cache and say nothing")
	}

	missing, claimed := disagreements(t, root)
	if len(missing) != 0 || len(claimed) != 0 {
		t.Fatalf("a built index disagrees with the store:"+
			"\n  the store holds and the index lacks: %v"+
			"\n  the index claims and the store lacks: %v", missing, claimed)
	}
}

// Rebuild replaces an index that is there and wrong.
//
// The repair, as against the migration above: opening fills a *missing* index
// and leaves a present one alone, because a present one is the one the engine
// has been maintaining. Replacing it is asked for.
func TestRebuildReplacesAnIndexThatIsWrong(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := Publish(root, ir.NodeID{1}, staged(t, root, ".real", "real"))
	if err != nil {
		t.Fatal(err)
	}

	// A record of a layer the store does not hold: the dangerous direction,
	// and the one a repair exists to remove.
	err = openIndex(t, root).Note(ir.NodeID{7})
	if err != nil {
		t.Fatal(err)
	}

	_, claimed := disagreements(t, root)
	if len(claimed) != 1 {
		t.Fatalf("the fixture did not produce a wrong index: claimed %v", claimed)
	}

	err = openIndex(t, root).Rebuild()
	if err != nil {
		t.Fatal(err)
	}

	missing, claimed := disagreements(t, root)
	if len(missing) != 0 || len(claimed) != 0 {
		t.Fatalf("a rebuilt index still disagrees with the store:"+
			"\n  the store holds and the index lacks: %v"+
			"\n  the index claims and the store lacks: %v", missing, claimed)
	}
}

// The zero index holds nothing, and is the only one a caller gets without asking.
//
// It is what OpenIndex returns for a store with no directory - a sandbox that
// has not started answers "" for its store (E141's neighbour), and joining that
// would read a stray `index/` beside whatever directory the process happens to
// be in. A wrong "no" costs a rebuild; a wrong "yes" is a cache hit against
// nothing, so the zero value takes the safe side of that.
func TestTheZeroIndexHoldsNothing(t *testing.T) {
	t.Parallel()

	if (Index{}).Has(ir.NodeID{1}) {
		t.Fatal("an index with no store answered yes")
	}

	err := (Index{}).Note(ir.NodeID{1})
	if err != nil {
		t.Fatalf("noting into an unset index failed rather than doing nothing: %v", err)
	}
}

// Forgetting a layer nobody recorded is not an error.
//
// Cleanup paths run more than once, and a second forget must not fail in a way
// that masks the first error - the same rule releasing an unknown handle
// follows.
func TestForgettingWhatWasNeverRecordedSucceeds(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := openIndex(t, root).Forget(ir.NodeID{9})
	if err != nil {
		t.Fatalf("forgetting an unrecorded layer failed: %v", err)
	}
}

// Two builds meeting an unindexed store both get an index, and it is complete.
//
// The migration happens on the first build after an upgrade, and there is no
// reason that is one build: a developer's shell and their editor's language
// server reach the same store at the same moment. Both walk, both write, and one
// of them renames onto a directory that now exists.
//
// Losing that is success, and only when filling a gap - the loser read the same
// store the winner did. The property that matters is the one asserted here: no
// caller sees a partial index, because the walk happens beside and arrives whole.
func TestTwoBuildsBuildingTheIndexAtOnceBothGetAWholeOne(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	for i, name := range []string{".r1", ".r2", ".r3", ".r4"} {
		err := Publish(root, ir.NodeID{byte(i + 1)}, staged(t, root, name, name))
		if err != nil {
			t.Fatal(err)
		}
	}

	err := os.RemoveAll(filepath.Join(root, "index"))
	if err != nil {
		t.Fatal(err)
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		bad  []error
		held [2]bool
	)

	for n := range 2 {
		wg.Go(func() {
			i, err := OpenIndex(root)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				bad = append(bad, err)

				return
			}

			held[n] = i.Has(ir.NodeID{4})
		})
	}

	wg.Wait()

	for _, err := range bad {
		t.Errorf("building the index concurrently failed: %v", err)
	}

	for n, ok := range held {
		if !ok {
			t.Errorf("build %d got an index that does not hold a layer the store does", n)
		}
	}

	missing, claimed := disagreements(t, root)
	if len(missing) != 0 || len(claimed) != 0 {
		t.Fatalf("two concurrent builds left an index that disagrees with the store:"+
			"\n  the store holds and the index lacks: %v"+
			"\n  the index claims and the store lacks: %v", missing, claimed)
	}
}

// Filling a gap that somebody else has already filled is success.
//
// The deterministic half of the test above, which cannot promise it reached this
// path: `fill` walks the store beside the index and renames the result in, so a
// second filler renames onto a directory that now exists. It read the same store
// and would have written the same answer, and the index it finds is whole.
//
// Only when filling a gap. A *replace* that finds one is a replace that did not
// happen, and reporting that as success would leave a wrong index in place with
// nobody told.
func TestFillingAGapAnotherFillerClosedSucceeds(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := Publish(root, ir.NodeID{1}, staged(t, root, ".g1", "g1"))
	if err != nil {
		t.Fatal(err)
	}

	err = os.RemoveAll(filepath.Join(root, "index"))
	if err != nil {
		t.Fatal(err)
	}

	i := Index{dir: root}

	err = i.fill(false)
	if err != nil {
		t.Fatal(err)
	}

	// The loser: the index it is about to install is already there.
	err = i.fill(false)
	if err != nil {
		t.Fatalf("filling a gap another filler had closed was reported as a failure: %v", err)
	}

	if !i.Has(ir.NodeID{1}) {
		t.Fatal("the index does not hold a layer the store does")
	}

	// Nothing left behind: a staging directory that outlives its fill is a
	// directory the next walk has to know is not a layer.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".index-") {
			t.Errorf("a fill left its staging directory behind: %s", e.Name())
		}
	}
}
