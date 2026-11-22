package store_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/store"
)

// TestAFindingOutlivesTheBuildThatMadeIt.
//
// **A record kept in a process is forgotten by the next build.** The step that
// wrote a credential into a layer runs once; every build after it gets that
// layer from the cache, never runs the step, never scans, and knows nothing -
// so the second build would save an image the first was refused.
//
// The finding therefore lives beside the layer, the way `.unmarked` records
// what a capture learned, and is read back by whoever is about to let the layer
// leave.
//
// The value is never written: this file is as durable as the layer and would
// outlive every rotation of the credential in it.
func TestAFindingOutlivesTheBuildThatMadeIt(t *testing.T) {
	t.Parallel()

	st := store.DirStore(t.TempDir())

	staged, err := st.Staging(".leaky-")
	if err != nil {
		t.Fatal(err)
	}

	id, err := st.Place(staged)
	if err != nil {
		t.Fatal(err)
	}

	// A layer nobody has said anything about is clean, and asking is cheap.
	if got := st.LeakedIn(id); len(got) != 0 {
		t.Fatalf("a fresh layer reports %v", got)
	}

	st.NoteLeaked(id, []string{"TOKEN in app.env", "DEPLOY_KEY in .netrc"})

	got := st.LeakedIn(id)
	if len(got) != 2 {
		t.Fatalf("read back %v, want both findings", got)
	}

	// Sorted, so two builds asking the same question are told the same thing in
	// the same order (I12).
	if got[0] > got[1] {
		t.Errorf("findings came back unsorted: %v", got)
	}

	if !strings.Contains(strings.Join(got, " "), "app.env") {
		t.Errorf("the finding lost where it was: %v", got)
	}

	// A second build reading the same store gets the same answer, which is the
	// whole point.
	again := store.DirStore(st.Root())
	if len(again.LeakedIn(id)) != 2 {
		t.Error("a fresh view of the store cannot see the finding, so a cached" +
			"\n  layer would be let out")
	}
}
