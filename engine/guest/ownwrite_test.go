package guest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// TestAStepsOwnOutputIsNotAnInput.
//
// **`printf > f && cat f` is everywhere**, and the read it makes is real: the
// tracer sees an `openat` and records the path as an input. The base cannot
// contain a file the step has just created, so the prediction naming it is
// stale on every later build - three of six in this repository's own build
// (E696).
//
// **The dangerous half is the second case.** `sed -i` on a base file also reads
// and writes the same path, and there the read *is* an input: dropping it is a
// false hit, which I3 forbids, and a false hit is worse than the miss being
// fixed.
//
// What tells them apart is not the delta - both paths are in it - but the base.
// A path the step read that is not below it was made by the step; one that is
// below it was read from there, whatever happened afterwards.
func TestAStepsOwnOutputIsNotAnInput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st := store.DirStore(dir)

	// A base holding one file, which is what `sed -i` would edit.
	staged, err := st.Staging(".base-")
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(staged, "from-base.txt"), []byte("original"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	base, err := st.Place(staged)
	if err != nil {
		t.Fatal(err)
	}

	// The step's own writes: one file it created, one it edited.
	delta := t.TempDir()

	for _, name := range []string{"made-here.txt", "from-base.txt"} {
		werr := os.WriteFile(filepath.Join(delta, name), []byte("written"), 0o600)
		if werr != nil {
			t.Fatal(werr)
		}
	}

	own := ownWrites(dir, []ir.NodeID{base}, delta)

	if !own("made-here.txt") {
		t.Error("a file the step created was treated as an input, so the" +
			"\n  prediction naming it is stale on every later build")
	}

	if own("from-base.txt") {
		t.Error("a file the step edited was treated as its own output; the read" +
			"\n  was of the base and dropping it is a false hit (I3)")
	}

	// A path in neither is not the step's own write either - it was read from
	// somewhere else entirely and is not this rule's business.
	if own("never-seen.txt") {
		t.Error("a path the step never wrote was called its own output")
	}
}

// With no base at all - a `FROM scratch` - everything in the delta was made by
// the step, which is the same rule and not a special case.
func TestWithNoBaseEverythingWrittenIsTheStepsOwn(t *testing.T) {
	t.Parallel()

	delta := t.TempDir()

	err := os.WriteFile(filepath.Join(delta, "f"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	own := ownWrites(t.TempDir(), nil, delta)
	if !own("f") {
		t.Error("with nothing below it, a written file is the step's own")
	}
}
