package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// What a stack answers for a path, across the ways a layer can decide it.
//
// **Three of these are ways a layer with none of the path's bytes still decides
// what the path is**: a marker deleting it, an opaque marker on its directory,
// and an opaque marker further up. They were written for an index that skipped
// layers it thought could not answer - see symlinkview_test.go for why there is
// no such index - and they are worth keeping without it, because getting any of
// them wrong does not fail a build. It produces a cache hit against a base that
// no longer says what the entry claims.
func TestTheIndexNeverChangesWhatAViewAnswers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	for _, c := range []struct {
		name    string
		layers  []map[string]string
		path    string
		present bool
	}{
		{
			name:    "found under many empty layers",
			layers:  []map[string]string{{"usr/bin/cat": "x"}, {}, {}, {}, {}, {}, {}, {}},
			path:    "/usr/bin/cat",
			present: true,
		},
		{
			name:    "a whiteout above hides it",
			layers:  []map[string]string{{"usr/bin/cat": "x"}, {"usr/bin/.wh.cat": ""}},
			path:    "/usr/bin/cat",
			present: false,
		},
		{
			name: "whiteout then re-added",
			layers: []map[string]string{
				{"usr/bin/cat": "x"}, {"usr/bin/.wh.cat": ""}, {"usr/bin/cat": "y"},
			},
			path:    "/usr/bin/cat",
			present: true,
		},
		{
			name: "an opaque directory above hides it",
			layers: []map[string]string{
				{"var/lib/thing": "x"}, {"var/lib/.wh..wh..opq": ""},
			},
			path:    "/var/lib/thing",
			present: false,
		},
		{
			name: "an opaque marker on a distant ancestor hides it",
			layers: []map[string]string{
				{"a/b/c/d/deep": "x"}, {"a/.wh..wh..opq": ""},
			},
			path:    "/a/b/c/d/deep",
			present: false,
		},
		{
			name: "an opaque layer that provides the path itself keeps it",
			layers: []map[string]string{
				{"var/lib/thing": "x"}, {"var/lib/.wh..wh..opq": "", "var/lib/thing": "y"},
			},
			path:    "/var/lib/thing",
			present: true,
		},
		{
			name:    "a path only in the topmost layer",
			layers:  []map[string]string{{}, {}, {}, {"top/only": "x"}},
			path:    "/top/only",
			present: true,
		},
		{
			name:    "a path in no layer at all",
			layers:  []map[string]string{{"a": "x"}, {"b": "y"}},
			path:    "/nowhere/at/all",
			present: false,
		},
		{
			name:    "a path with dots in it",
			layers:  []map[string]string{{"etc/conf.d/my.conf": "x"}, {}},
			path:    "/etc/conf.d/my.conf",
			present: true,
		},
		{
			name:    "a deep path under many layers",
			layers:  []map[string]string{{"a/b/c/d/e/f/g/h": "x"}, {}, {}, {}, {}, {}},
			path:    "/a/b/c/d/e/f/g/h",
			present: true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			store := t.TempDir()
			stack := make([]ir.NodeID, 0, len(c.layers))

			for i, files := range c.layers {
				stack = append(stack, layerAt(t, store, fmt.Sprintf("%s-%d", c.name, i), files))
			}

			view, err := LayerStore(store).View(ctx, stack)
			if err != nil {
				t.Fatal(err)
			}

			_, got := view.Digest(c.path)
			if got != c.present {
				t.Errorf("Digest(%q) present=%v, want %v", c.path, got, c.present)
			}
		})
	}
}

// Two stacks sharing a layer each see it, and neither sees the other's.
func TestAnIndexIsSharedBetweenStacks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := t.TempDir()

	shared := layerAt(t, store, "shared", map[string]string{"common/file": "x"})
	onlyA := layerAt(t, store, "a", map[string]string{"a/file": "x"})
	onlyB := layerAt(t, store, "b", map[string]string{"b/file": "x"})

	a, err := LayerStore(store).View(ctx, []ir.NodeID{shared, onlyA})
	if err != nil {
		t.Fatal(err)
	}

	b, err := LayerStore(store).View(ctx, []ir.NodeID{shared, onlyB})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := a.Digest("/common/file"); !ok {
		t.Error("the shared layer's file is missing from the first stack")
	}

	if _, ok := b.Digest("/common/file"); !ok {
		t.Error("the shared layer's file is missing from the second stack")
	}

	if _, ok := a.Digest("/b/file"); ok {
		t.Error("a stack answered for a layer it does not contain")
	}

	if _, ok := b.Digest("/a/file"); ok {
		t.Error("a stack answered for a layer it does not contain")
	}
}
