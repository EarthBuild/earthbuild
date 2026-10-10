package guest

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// **Where a copy put something is recorded by the copy that put it there.**
//
// Deciding that `COPY --dir src /code/` lands at /code/src and `COPY src /code/`
// lands at /code is `placedAs` and `intoDir` reading a working directory, a
// trailing separator and the source's own kind. Anything downstream that needs
// to map a path inside a step back to the layer it came from - a job-level skip
// key, docs-internals/job-skipping.md - must be told rather than work it out
// again, because a second implementation of that rule is the defect this
// repository keeps a key guard for.
//
// So these assertions are the same four cases `copydir_test.go` checks the
// *filesystem* for, checked against what was written down about it. If the two
// ever disagree, the record is wrong and the skip built on it is unsafe.
func TestACopyRecordsWhereItPutThings(t *testing.T) {
	t.Parallel()

	for _, one := range []struct {
		name string
		src  string
		dest string
		opts copyOpts
		want core.Placement
	}{
		{
			name: "a directory contributes its contents",
			src:  "src", dest: "/code/",
			want: core.Placement{Layer: testSrcLayer, From: "src", To: "/code"},
		},
		{
			name: "--dir places the directory itself",
			src:  "src", dest: "/code/", opts: copyOpts{AsDir: true},
			want: core.Placement{Layer: testSrcLayer, From: "src", To: "/code/src"},
		},
		{
			name: "--dir into a destination that does not exist",
			src:  "src", dest: "/placed", opts: copyOpts{AsDir: true},
			want: core.Placement{Layer: testSrcLayer, From: "src", To: "/placed"},
		},
	} {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()

			s, h := copyDirFixture(t)

			err := s.copyIn(h, []string{testSrcLayer}, one.src, one.dest, one.opts)
			if err != nil {
				t.Fatal(err)
			}

			got := s.placementsOf(h)
			if len(got) != 1 {
				t.Fatalf("the copy recorded %d placements, want 1: %v", len(got), got)
			}

			if got[0] != one.want {
				t.Errorf("recorded %+v, want %+v", got[0], one.want)
			}
		})
	}
}

// A copy from a layer the build produced is recorded too, and is not a context:
// whoever reads these decides which layers are contexts, because only the plan
// knows.
func TestAPlacementNamesTheLayerItCameFrom(t *testing.T) {
	t.Parallel()

	s, h := copyDirFixture(t)

	err := s.copyIn(h, []string{testSrcLayer}, "src", "/code/", copyOpts{AsDir: true})
	if err != nil {
		t.Fatal(err)
	}

	got := s.placementsOf(h)
	if len(got) != 1 || got[0].Layer != testSrcLayer {
		t.Errorf("the placement names layer %q, want %q", got[0].Layer, testSrcLayer)
	}
}

// Nothing copied, nothing recorded - and asking about a handle no copy touched
// is not an error.
func TestAHandleWithNoCopiesHasNoPlacements(t *testing.T) {
	t.Parallel()

	s, h := copyDirFixture(t)

	if got := s.placementsOf(h); len(got) != 0 {
		t.Errorf("a handle nothing copied into has placements: %v", got)
	}
}

// **The record has to cross the wire**, because the copy happens in the guest
// and the key is derived on the host.
//
// A separate request rather than a field on the observation pages: an
// observation is fetched in pages and a placement is not part of one, so riding
// along would mean deciding which page carries it and what an older guest does
// with the answer. Asked for on its own, a guest that does not know the question
// says so and the host falls back to the coarser key - which is the direction a
// failure here has to fail.
func TestPlacementsCrossTheWire(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := storeLayer(t, dir, map[string]string{"tree/a.txt": "one"})

	root, delta := t.TempDir(), t.TempDir()

	s := &Server{LayerDir: dir, Mat: &overlayMat{root: root, delta: delta}, Unconfined: true}
	ctx := t.Context()

	got := s.handle(ctx, Request{Kind: KindMaterialise, Stack: []string{src.String()}}, nil)
	if got.Err != "" {
		t.Fatalf("materialise: %s", got.Err)
	}

	handle := got.Handle

	got = s.handle(ctx, Request{
		Kind: KindCopy, Handle: handle, From: []string{src.String()},
		Path: "tree", Dest: "/w/", DirCopy: true,
	}, nil)
	if got.Err != "" {
		t.Fatalf("copy: %s", got.Err)
	}

	got = s.handle(ctx, Request{Kind: KindPlacements, Handle: handle}, nil)
	if got.Err != "" {
		t.Fatalf("placements: %s", got.Err)
	}

	want := core.Placement{Layer: src.String(), From: "tree", To: "/w/tree"}
	if len(got.Placed) != 1 || got.Placed[0] != want {
		t.Errorf("the wire carried %+v, want one %+v", got.Placed, want)
	}
}

// A handle nobody copied into answers with none rather than refusing: a build
// with no COPY from a context is an ordinary build, not a broken one.
func TestPlacementsForAnUntouchedHandleAreNone(t *testing.T) {
	t.Parallel()

	root, delta := t.TempDir(), t.TempDir()
	s := &Server{LayerDir: t.TempDir(), Mat: &overlayMat{root: root, delta: delta}, Unconfined: true}

	got := s.handle(t.Context(), Request{Kind: KindMaterialise}, nil)
	if got.Err != "" {
		t.Fatalf("materialise: %s", got.Err)
	}

	got = s.handle(t.Context(), Request{Kind: KindPlacements, Handle: got.Handle}, nil)
	if got.Err != "" || len(got.Placed) != 0 {
		t.Errorf("an untouched handle answered %+v, err %q", got.Placed, got.Err)
	}
}
