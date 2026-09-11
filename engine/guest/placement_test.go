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
