package guest

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/mat/overlay"
)

// A view of an earlier result shows that result's whole filesystem.
//
// §3.3d with ν ∈ 𝕂. The property that makes this different from a view of the
// context: a stage is a *stack* of layers, so what the step sees has to be the
// merge of them - the later layer's version of a path winning - and not the
// topmost layer on its own. A view assembled wrongly shows a filesystem missing
// everything the base contributed, which looks like a build error inside the
// step rather than a mount that was never assembled.
//
// The same materialiser that builds a step's own base builds this, which is
// what keeps the two definitions of "a stage's filesystem" from drifting.
//
// **Skips where TMPDIR is itself overlayfs**, which a container's usually is -
// overlayfs will not stack on overlayfs. That makes the obvious way to run
// these report success while testing nothing, so:
//
//	docker run --privileged --tmpfs /work:exec -e TMPDIR=/work ...
//
// is what actually exercises them, and is how they were.
func TestAViewOfAStageShowsTheWholeStack(t *testing.T) {
	dir := t.TempDir()

	m, err := overlay.New(dir)
	if err != nil {
		t.Skipf("no materialiser here: %v", err)
	}

	lower, upper := ir.NodeID{1}, ir.NodeID{2}

	err = m.WriteLayer(lower, map[string]string{"from-base": "yes", "shared": "base"})
	if err != nil {
		t.Fatal(err)
	}

	err = m.WriteLayer(upper, map[string]string{"from-top": "yes", "shared": "top"})
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{Mat: m, LayerDir: dir}

	got, release, err := s.resolveStacks(context.Background(), []Mount{
		{Target: "/view", Stack: []string{lower.String(), upper.String()}, ReadOnly: true},
	})
	if err != nil {
		t.Skipf("this machine cannot assemble a view: %v", err)
	}

	defer release()

	if got[0].Sandbox == "" {
		t.Fatal("the view was not resolved to a path, so nothing can bind it")
	}

	if len(got[0].Stack) != 0 {
		t.Error("the stack survived resolution and would be assembled twice")
	}

	for name, want := range map[string]string{
		"from-base": "yes",
		"from-top":  "yes",
		// The later layer wins, which is what stacking means.
		"shared": "top",
	} {
		b, readErr := readAll(filepath.Join(got[0].Sandbox, name))
		if readErr != nil {
			t.Errorf("%s is not in the view: %v", name, readErr)

			continue
		}

		if b != want {
			t.Errorf("%s reads %q, want %q", name, b, want)
		}
	}
}

// A view of a subtree of a stage, rather than all of it.
func TestAViewOfAStageCanBeASubtree(t *testing.T) {
	dir := t.TempDir()

	m, err := overlay.New(dir)
	if err != nil {
		t.Skipf("no materialiser here: %v", err)
	}

	id := ir.NodeID{3}

	err = m.WriteLayer(id, map[string]string{"inner/f": "deep"})
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{Mat: m, LayerDir: dir}

	got, release, err := s.resolveStacks(context.Background(), []Mount{
		{Target: "/view", Stack: []string{id.String()}, Sub: "inner", ReadOnly: true},
	})
	if err != nil {
		t.Skipf("this machine cannot assemble a view: %v", err)
	}

	defer release()

	b, err := readAll(filepath.Join(got[0].Sandbox, "f"))
	if err != nil {
		t.Fatalf("the subtree is not at the resolved path: %v", err)
	}

	if b != "deep" {
		t.Errorf("the view reads %q", b)
	}
}
