package cacheshare

import (
	"bytes"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A mount that was bound and shared nothing says why, once.
//
// **Silence is right for one case here and wrong for the rest.** A mount no
// step bound has no directory, which is most mounts on most builds, and
// `TestASightedMachineIsSilent` holds that line. A directory that exists and
// yields nothing is the opposite: a step did bind this mount and the sharing
// still came to nothing, and a quiet return there is indistinguishable from
// not having the feature at all - an author writes `--portable-except` and
// `--helper`, gets no sharing, and has nowhere to start.
//
// Tested from inside the package because the path it guards needs a real wasm
// helper to reach, and every unit fixture here deliberately supplies bytes that
// fail to compile. The end-to-end proof is a build of
// `examples/cache-helpers/go-build+compile`.
func TestSharingNothingNamesTheCauseOnce(t *testing.T) {
	t.Parallel()

	var said bytes.Buffer

	s := New(t.TempDir(), "", &said)
	m := ir.Mount{ID: "k", Portable: true}

	// Once per directory: forty steps over one cache is one line, not forty.
	for range 5 {
		if err := s.nothing(m, "/mounts/k/abc", "a step bound this mount and wrote nothing into it"); err != nil {
			t.Fatalf("explaining a quiet cache failed the build: %v", err)
		}
	}

	if got := strings.Count(said.String(), "nothing to share"); got != 1 {
		t.Errorf("said it %d times, want once per directory:\n%s", got, said.String())
	}

	for _, want := range []string{"k", "/mounts/k/abc", "wrote nothing"} {
		if !strings.Contains(said.String(), want) {
			t.Errorf("the reason does not mention %q: %q", want, strings.TrimSpace(said.String()))
		}
	}

	// A different cache is a different story and gets its own line.
	if err := s.nothing(ir.Mount{ID: "j"}, "/mounts/j/abc", "the helper recognised no units"); err != nil {
		t.Fatal(err)
	}

	if got := strings.Count(said.String(), "nothing to share"); got != 2 {
		t.Errorf("a second cache was folded into the first: %q", said.String())
	}
}

// And a machine with nowhere to report to does not crash trying.
func TestSharingNothingNeedsNoWriter(t *testing.T) {
	t.Parallel()

	s := New(t.TempDir(), "", nil)

	if err := s.nothing(ir.Mount{ID: "k"}, "/x", "why"); err != nil {
		t.Errorf("a silent sharing returned %v", err)
	}
}
