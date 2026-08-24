//go:build linux

package overlay

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/decl"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A stack element that declares is folded into the environment, not stacked
// into the filesystem.
//
// Green paper §3.2a: most elements contribute paths and some contribute only
// what an image says about how a step runs. Both are elements, so both travel
// and both reach a key through ids(𝑏) - which is the whole point of modelling a
// declaration this way rather than as a file beside a layer.
func TestADeclarationIsFoldedRatherThanStacked(t *testing.T) {
	t.Parallel()

	m, root := materialiserFor(t)

	layer := ir.NodeID{1}
	err := m.WriteLayer(layer, map[string]string{"in-the-tree": "yes"})
	if err != nil {
		t.Fatal(err)
	}

	id, err := decl.Write(root, decl.Declaration{Env: []string{"GOPATH=/go", "PATH=/go/bin"}})
	if err != nil {
		t.Fatal(err)
	}

	h, err := m.Materialise(context.Background(), []ir.NodeID{layer, id})
	if err != nil {
		t.Skipf("this machine cannot mount overlayfs: %v", err)
	}

	defer func() { _ = h.Release() }()

	// The tree is the layer's, and the declaration put nothing in it.
	if _, err := os.Stat(filepath.Join(h.Root(), "in-the-tree")); err != nil {
		t.Errorf("the layer's file is missing: %v", err)
	}

	entries, err := os.ReadDir(h.Root())
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}

		t.Errorf("the merged tree holds %v, want only the layer's file", names)
	}

	// And the declaration reached the environment.
	d, ok := h.(interface{ Declared() []string })
	if !ok {
		t.Fatal("the handle cannot report what the stack declared")
	}

	if got := d.Declared(); !slices.Contains(got, "GOPATH=/go") {
		t.Errorf("declared %v, want it to carry GOPATH", got)
	}
}

// An element the store holds neither way is refused, not invented.
//
// I18. `MkdirAll` used to make a directory for whatever was not there, so a
// layer that never arrived materialised as one contributing nothing - which is
// indistinguishable from a declaration, and is the silent-wrong-answer shape
// this whole mechanism exists to remove.
func TestAMissingElementIsRefused(t *testing.T) {
	t.Parallel()

	m, _ := materialiserFor(t)

	absent := ir.NodeID{9, 9, 9}

	_, err := m.Materialise(context.Background(), []ir.NodeID{absent})
	if err == nil {
		t.Fatal("a stack naming an element this store does not hold was materialised anyway")
	}

	for _, want := range []string{absent.String(), "neither"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n  %v", want, err)
		}
	}
}

func materialiserFor(t *testing.T) (*Materialiser, string) {
	t.Helper()

	root := t.TempDir()

	m, err := NewSplit(root, t.TempDir())
	if err != nil {
		t.Fatalf("prepare a materialiser: %v", err)
	}

	return m, root
}
