package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/decl"
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
		b, err := os.ReadFile(filepath.Join(got, filepath.FromSlash(tc.path)))
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

// A declaration in the range is skipped, not mistaken for a lost layer.
//
// An image's environment travels as a stack element so a worker fetching the
// stack fetches it too, and it is stored as `layers/<id>.decl` - a file, where
// this wants a tree. Φ collapses a *range* of the stack, so a declaration falls
// inside it whenever the base of a squashed stack declares anything, and every
// such build stopped with the declaration reported as a layer the store had
// lost (E749, E751).
func TestSquashingARangeThatCarriesADeclaration(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	lower, upper, into := ir.NodeID{1}, ir.NodeID{2}, ir.NodeID{3}

	writeLayer(t, store, lower, map[string]string{"a": "from the lower"})
	writeLayer(t, store, upper, map[string]string{"b": "from the upper"})

	declares, err := decl.Write(store, decl.Declaration{Env: []string{"PATH=/usr/bin"}})
	if err != nil {
		t.Fatal(err)
	}

	// Above the layer it came with, which is where the scheduler puts it.
	err = squashInto(context.Background(), store, into, []ir.NodeID{lower, declares, upper})
	if err != nil {
		t.Fatalf("a range carrying a declaration was refused: %v", err)
	}

	// Both layers merged, the declaration contributing nothing to the tree.
	for name, want := range map[string]string{"a": "from the lower", "b": "from the upper"} {
		got, readErr := os.ReadFile(filepath.Join(store, "layers", into.String(), name))
		if readErr != nil {
			t.Fatalf("%s did not reach the squashed layer: %v", name, readErr)
		}

		if string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// A layer the store really has not got is still refused.
//
// The declaration skip must not become a general tolerance for absence: a stack
// naming a layer that is not there is a build whose result would be missing
// files, and the whole value of the check is that it says so (E751).
func TestSquashingStillRefusesALayerThatIsNotThere(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	present, absent, into := ir.NodeID{1}, ir.NodeID{9}, ir.NodeID{3}

	writeLayer(t, store, present, map[string]string{"a": "here"})

	err := squashInto(context.Background(), store, into, []ir.NodeID{present, absent})
	if err == nil {
		t.Fatal("a range naming a layer the store does not hold was squashed")
	}
}
