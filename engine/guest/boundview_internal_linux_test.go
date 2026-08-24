package guest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/nstest"
)

// A bound view shows a layer this build made, and the step cannot write to it.
//
// Green paper §3.3d. Two properties, and both matter for a different reason:
//
// It resolves against the **layer** store, not the cache store. They are
// different directories, and a view resolved against the wrong one is a step
// reading an empty directory rather than the object it asked for - which fails
// inside the step, saying nothing about mounts.
//
// It is **read-only**, which is what makes a bound view admissible at all
// (I20). The layer store is shared by every step standing on it, so a step
// writing through this would edit another step's input - the one thing a
// content-addressed store cannot survive (§3.3b).
func TestABoundViewShowsALayerAndCannotBeWrittenThrough(t *testing.T) {
	// Binding needs a namespace this process is root in, which CI's unit-test
	// container is not - `mount: operation not permitted` is what that looks
	// like. nstest re-runs this test inside one, which is what every other
	// mount test here does.
	if !nstest.In(t) {
		return
	}

	root, cache, layers := t.TempDir(), t.TempDir(), t.TempDir()

	const id = "0123456789abcdef"

	at := filepath.Join(layers, "layers", id)

	err := os.MkdirAll(filepath.Join(at, "inner"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(at, "inner", "f"), []byte("from the layer"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	undo, err := bindMounts(root, cache, layers, []Mount{
		{Target: "/view", Layer: id, ReadOnly: true},
	})
	if err != nil {
		t.Fatalf("binding a view of %s: %v", id, err)
	}

	defer undo()

	got, err := os.ReadFile(filepath.Join(root, "view", "inner", "f"))
	if err != nil {
		t.Fatalf("the view does not show the layer: %v", err)
	}

	if string(got) != "from the layer" {
		t.Errorf("the view shows %q, not what the layer holds", got)
	}

	// Through the target, because that is the door the step has.
	err = os.WriteFile(filepath.Join(root, "view", "inner", "g"), []byte("no"), 0o600)
	if err == nil {
		t.Error("a step wrote through a bound view, which edits another step's" +
			" input; the layer store is read-only to a step (§3.3b)")
	}
}

// A subtree of a layer, rather than all of it.
//
// 𝑢 of §3.3d. A Dockerfile writes `--mount=source=/tmp/.ldflags,...` and means
// one path inside the object, not the object - so binding the whole of it would
// put a tree where a file was expected.
func TestABoundViewCanShowASubtree(t *testing.T) {
	if !nstest.In(t) {
		return
	}

	root, cache, layers := t.TempDir(), t.TempDir(), t.TempDir()

	const id = "fedcba9876543210"

	at := filepath.Join(layers, "layers", id, "inner")

	err := os.MkdirAll(at, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(at, "f"), []byte("subtree"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	undo, err := bindMounts(root, cache, layers, []Mount{
		{Target: "/view", Layer: id, Sub: "inner", ReadOnly: true},
	})
	if err != nil {
		t.Fatalf("binding a subtree: %v", err)
	}

	defer undo()

	got, err := os.ReadFile(filepath.Join(root, "view", "f"))
	if err != nil {
		t.Fatalf("the subtree is not at the mount point: %v", err)
	}

	if string(got) != "subtree" {
		t.Errorf("the view shows %q", got)
	}
}
