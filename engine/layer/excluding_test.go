package layer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A file faulted in is not something the step wrote.
//
// **The obstacle in the way of a lazy base**, and the way round it. A step's
// delta is where its writes land; a base is what it reads. Overlayfs keeps them
// apart by having two directories - and a lazily materialised base cannot use
// that, because a lowerdir may not change under a live mount and a fault-in is
// precisely a change to the base while the step is running (E293).
//
// So a faulted-in file lands in the *upper* directory with the step's writes, and
// the capture leaves it out - by name and by digest, both, because the engine
// knows exactly what it put there.
func TestAFileFaultedInIsNotSomethingTheStepWrote(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// What the step wrote.
	err := os.WriteFile(filepath.Join(root, "output"), []byte("made by the step\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	// What the engine faulted in for it.
	faulted := []byte("from the base\n")

	err = os.WriteFile(filepath.Join(root, "libc.so"), faulted, 0o644)
	if err != nil {
		t.Fatal(err)
	}

	with, err := layer.Take(root)
	if err != nil {
		t.Fatal(err)
	}

	without, err := layer.TakeExcluding(root, map[string]ir.NodeID{
		"libc.so": layer.ContentID(faulted),
	})
	if err != nil {
		t.Fatal(err)
	}

	if with.ID == without.ID {
		t.Fatal("excluding a faulted-in file changed nothing" +
			"\n  the step's layer would contain a copy of its own base")
	}

	// And it is the same layer the step would have produced with a whole base.
	only := t.TempDir()

	err = os.WriteFile(filepath.Join(only, "output"), []byte("made by the step\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Chtimes(filepath.Join(only, "output"),
		mtimeOf(t, filepath.Join(root, "output")),
		mtimeOf(t, filepath.Join(root, "output")))
	if err != nil {
		t.Fatal(err)
	}

	err = os.Chtimes(only, mtimeOf(t, root), mtimeOf(t, root))
	if err != nil {
		t.Fatal(err)
	}

	want, err := layer.Take(only)
	if err != nil {
		t.Fatal(err)
	}

	if without.ID != want.ID {
		t.Errorf("the excluded capture is %v and a step with a whole base"+
			" produces %v\n  a lazily materialised step must produce the same"+
			" layer as an eagerly materialised one, or the cache is a lottery",
			without.ID, want.ID)
	}
}

// A faulted-in file the step then changed is the step's.
//
// The subtlety that makes this safe. A step may open a file from its base, read
// it, and then write it - `sed -i` on a config, a compiler updating a cache. That
// file is genuinely part of the delta, and excluding it by name alone would lose
// a real write.
//
// So the exclusion is by name **and** digest: it is dropped only if it is still
// exactly what this engine put there.
func TestAFaultedInFileTheStepChangedIsTheStepsAfterAll(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	faulted := []byte("from the base\n")

	err := os.WriteFile(filepath.Join(root, "config"), faulted, 0o644)
	if err != nil {
		t.Fatal(err)
	}

	// The step edits it.
	err = os.WriteFile(filepath.Join(root, "config"), []byte("edited\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	got, err := layer.TakeExcluding(root, map[string]ir.NodeID{
		"config": layer.ContentID(faulted),
	})
	if err != nil {
		t.Fatal(err)
	}

	whole, err := layer.Take(root)
	if err != nil {
		t.Fatal(err)
	}

	if got.ID != whole.ID {
		t.Error("a file the step edited was dropped as a fault-in" +
			"\n  the step's own write is missing from its layer")
	}
}

// Excluding nothing is Take.
func TestExcludingNothingIsAnOrdinaryCapture(t *testing.T) {
	t.Parallel()

	root := tree(t)

	whole, err := layer.Take(root)
	if err != nil {
		t.Fatal(err)
	}

	same, err := layer.TakeExcluding(root, nil)
	if err != nil {
		t.Fatal(err)
	}

	if whole.ID != same.ID {
		t.Errorf("excluding nothing gave %v, want %v", same.ID, whole.ID)
	}
}

func mtimeOf(t *testing.T, p string) time.Time {
	t.Helper()

	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}

	return fi.ModTime()
}

// A step that deletes a file from a lazy base is refused, not quietly wrong.
//
// **The hole an overlay would have covered.** In an overlay a step that unlinks a
// base file leaves a whiteout in the upper directory, and the layer says "this is
// gone". A lazy base has no overlay (E293): the file is simply absent, the
// capture sees nothing where something used to be, and the layer says **nothing
// at all** - so materialising base plus delta still shows the file.
//
// The step succeeded. The layer is real. And it means something different from
// what happened, which is the worst kind of wrong this engine can produce.
//
// So it refuses. Refusing costs a build that could have worked; the alternative
// costs a cache entry that is wrong for ever (I10, E294).
func TestAStepDeletingFromALazyBaseIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	faulted := []byte("from the base\n")
	at := filepath.Join(root, "libc.so")

	err := os.WriteFile(at, faulted, 0o644)
	if err != nil {
		t.Fatal(err)
	}

	// The step deletes it.
	err = os.Remove(at)
	if err != nil {
		t.Fatal(err)
	}

	_, err = layer.TakeExcluding(root, map[string]ir.NodeID{
		"libc.so": layer.ContentID(faulted),
	})
	if err == nil {
		t.Fatal("a deletion from a lazy base was captured as though nothing" +
			" had happened\n  the layer means something different from what the" +
			" step did")
	}

	if !strings.Contains(err.Error(), "libc.so") {
		t.Errorf("%v\n  the message must name what was deleted", err)
	}
}

// A directory made to hold a faulted-in file is not the step's either.
//
// **Found by running the whole thing** (E306). Priming a base and faulting into
// it creates the directories the files live in - `etc/`, `usr/`, `usr/lib/` - and
// in an overlay none of those would exist in the delta, because reading a base
// file creates nothing in the upper.
//
// So they are excluded too, by name, with a zero digest saying "this is a
// directory the engine placed". A step that makes the same directory itself
// loses nothing: the base already has it, which is why the engine made it, so
// the delta need not record it.
func TestADirectoryMadeForAFaultedInFileIsNotTheSteps(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// What priming left behind.
	must(t, os.MkdirAll(filepath.Join(root, "usr", "lib"), 0o755))

	faulted := []byte("from the base\n")
	must(t, os.WriteFile(filepath.Join(root, "usr", "lib", "libc.so"), faulted, 0o644))

	// What the step wrote.
	must(t, os.WriteFile(filepath.Join(root, "out"), []byte("made\n"), 0o644))

	got, err := layer.TakeExcluding(root, map[string]ir.NodeID{
		"usr":             {},
		"usr/lib":         {},
		"usr/lib/libc.so": layer.ContentID(faulted),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Only the step's own write, and the root's own entry for it.
	only := t.TempDir()
	must(t, os.WriteFile(filepath.Join(only, "out"), []byte("made\n"), 0o644))
	must(t, os.Chtimes(filepath.Join(only, "out"), modOf(t, filepath.Join(root, "out")),
		modOf(t, filepath.Join(root, "out"))))
	must(t, os.Chtimes(only, modOf(t, root), modOf(t, root)))

	want, err := layer.Take(only)
	if err != nil {
		t.Fatal(err)
	}

	if got.ID != want.ID {
		t.Errorf("the directories priming made are in the step's layer: %v"+
			" against %v", got.ID, want.ID)
	}
}

func modOf(t *testing.T, p string) time.Time {
	t.Helper()

	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}

	return fi.ModTime()
}
