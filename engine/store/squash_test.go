package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// writeLayer writes a layer directory with the given files, for these tests.
func writeLayer(t *testing.T, store string, id ir.NodeID, files map[string]string) {
	t.Helper()

	dir := filepath.Join(store, "layers", id.String())

	for name, content := range files {
		p := filepath.Join(dir, name)

		err := os.MkdirAll(filepath.Dir(p), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(p, []byte(content), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// A squashed layer is the range, newest winning, exactly as a mount would be.
//
// Φ collapses the oldest layers of a stack into one so the rest can be mounted
// (green paper 4.8). The result stands in for what those layers meant together,
// so it has to *be* what they meant together: a file written twice belongs to
// the later writer, and a file written once belongs in the result whichever
// layer wrote it.
//
// Getting this wrong is undetectable at the mount - the directory exists and
// the mount succeeds - and shows up as a base image missing half its files.
func TestASquashedLayerIsTheRangeMerged(t *testing.T) {
	t.Parallel()

	store := t.TempDir()

	a, b, c := ir.NodeID{1}, ir.NodeID{2}, ir.NodeID{3}

	writeLayer(t, store, a, map[string]string{
		"only-in-a.txt":   "a\n",
		testTwiceFile:     "from a\n",
		"nested/deep.txt": "deep a\n",
	})
	writeLayer(t, store, b, map[string]string{
		"only-in-b.txt": "b\n",
		testTwiceFile:   "from b\n",
	})
	writeLayer(t, store, c, map[string]string{"only-in-c.txt": "c\n"})

	rng := []ir.NodeID{a, b, c}
	into := core.SquashID(rng)

	err := squashInto(context.Background(), store, into, rng)
	if err != nil {
		t.Fatal(err)
	}

	got := filepath.Join(store, "layers", into.String())

	for _, tc := range []struct{ path, want string }{
		{"only-in-a.txt", "a\n"},
		{"only-in-b.txt", "b\n"},
		{"only-in-c.txt", "c\n"},
		{"nested/deep.txt", "deep a\n"},
		// The later layer wins, which is the whole of overlayfs's semantics
		// reduced to one file.
		{testTwiceFile, "from b\n"},
	} {
		b, err := os.ReadFile(filepath.Join(got, filepath.FromSlash(tc.path))) //nolint:gosec // a fixture this test wrote
		if err != nil {
			t.Errorf("%s is missing from the squashed layer: %v", tc.path, err)

			continue
		}

		if string(b) != tc.want {
			t.Errorf("%s is %q, want %q", tc.path, string(b), tc.want)
		}
	}
}

// Squashing the same range twice does the work once.
//
// The identity is derived from the range, so the second call is a build that
// has already happened. It has to be cheap *and* it has to not corrupt the
// first result - a rebuild that half-replaced a layer another step is mounting
// at that moment would be worse than slow.
func TestSquashingTwiceIsSquashingOnce(t *testing.T) {
	t.Parallel()

	store := t.TempDir()

	a, b := ir.NodeID{4}, ir.NodeID{5}
	writeLayer(t, store, a, map[string]string{"f.txt": "one\n"})
	writeLayer(t, store, b, map[string]string{"g.txt": "two\n"})

	rng := []ir.NodeID{a, b}
	into := core.SquashID(rng)

	err := squashInto(context.Background(), store, into, rng)
	if err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(store, "layers", into.String())

	// A marker nothing should touch: if the second call rebuilds, it is gone.
	err = os.WriteFile(filepath.Join(dir, "marker"), []byte("kept"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = squashInto(context.Background(), store, into, rng)
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(filepath.Join(dir, "marker"))
	if err != nil {
		t.Error("the second squash rebuilt a layer that was already there")
	}
}

// A half-built squash is never a layer.
//
// The store is read by other steps of the same build, concurrently, and a
// directory that exists is a directory that will be mounted. So the merge
// happens beside the final name and arrives by rename - the same rule the layer
// writer already follows, for the same reason.
func TestAnInterruptedSquashLeavesNoLayer(t *testing.T) {
	t.Parallel()

	store := t.TempDir()

	a := ir.NodeID{6}
	writeLayer(t, store, a, map[string]string{"f.txt": "one\n"})

	// A range naming a layer that is not in the store: the merge must fail.
	missing := ir.NodeID{7}
	rng := []ir.NodeID{a, missing}
	into := core.SquashID(rng)

	err := squashInto(context.Background(), store, into, rng)
	if err == nil {
		t.Fatal("a range naming a layer that is not there was squashed anyway")
	}

	_, err = os.Stat(filepath.Join(store, "layers", into.String()))
	if err == nil {
		t.Error("a failed squash left a layer behind, which a later mount would use")
	}
}
