package fleet

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// An answer that is not the fragment asked for is refused, not returned.
//
// A peer that cannot fragment answers with the whole blob. Taking it as a
// fragment would hand the caller bytes it did not ask for and call them the part
// it wanted - I10 is about naming a gap rather than papering over it.
//
// Written because the mutant for this survived once it could compile: the
// catalogue had it deleting a line that orphaned a variable, so for as long as
// anybody had looked, the verdict was NOCOMPILE and the gap was invisible.
func TestAnAnswerThatIsNotAFragmentIsRefused(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	// **A well-formed body behind the wrong flag**, which is the fixture that
	// asks the question. An answer that is merely truncated is refused by the
	// next read whether the flag is checked or not, so it cannot tell whether
	// the flag was checked at all - and a mutant that removed the check
	// survived against exactly that.
	//
	// Flag 1 is a whole blob: what a peer that cannot fragment sends.
	err := WriteMessage(&buf, []byte{1})
	if err != nil {
		t.Fatal(err)
	}

	small, err := squeeze([]byte("a manifest"))
	if err != nil {
		t.Fatal(err)
	}

	err = WriteBlobMessage(&buf, small)
	if err != nil {
		t.Fatal(err)
	}

	err = WriteBlobMessage(&buf, []byte("the packed bytes"))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = readFragment(&buf, ir.NodeID{1})
	if err == nil {
		t.Fatal("a whole blob was accepted as the fragment that was asked for")
	}

	if !errors.Is(err, ErrMalformed) {
		t.Errorf("the refusal is %v, which callers cannot tell from a transport"+
			" failure", err)
	}
}

// The read-set hint is ordered, so it does not vary run to run.
//
// A fragment is named by the paths it holds (E282), so a hint built by walking a
// map would name one fragment differently on every build - and two builds of the
// same step would never share a transfer.
//
// Also a survivor once its mutant compiled: the catalogue deleted the sort and
// took the `sort` import with it.
func TestTheReadSetHintDoesNotVaryRunToRun(t *testing.T) {
	t.Parallel()

	reads := map[string]ir.NodeID{}
	for i, p := range []string{"z", "m", "a", "q", "b", "y", "c", "n"} {
		reads[p] = ir.NodeID{byte(i + 1)}
	}

	n := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"make"}}}

	hint := readsFrom(profilesHolding(t, n, reads))
	if hint == nil {
		t.Skip("this build has no observations to hint from")
	}

	first := hint(n)
	if len(first) < 2 {
		t.Skipf("the hint named %d paths, which cannot show an order", len(first))
	}

	if !slices.IsSorted(first) {
		t.Errorf("the hint is %v, which is a map's order and not an order", first)
	}

	// Asked again, because a single sorted answer could be luck.
	for range 8 {
		if got := hint(n); !slices.Equal(got, first) {
			t.Fatalf("the hint answered %v and then %v; a fragment named this"+
				" way is a different fragment on every build", first, got)
		}
	}
}

// profilesHolding is a Profiles that reports one step class's reads.
func profilesHolding(t *testing.T, n *ir.Node, reads map[string]ir.NodeID) core.Profiles {
	t.Helper()

	return fakeProfiles{class: core.StepClass(n), reads: reads}
}

type fakeProfiles struct {
	reads map[string]ir.NodeID
	class core.Key
}

func (f fakeProfiles) Get(class core.Key) (core.Observation, bool) {
	if class != f.class {
		return core.Observation{}, false
	}

	return core.Observation{Reads: f.reads}, true
}

func (f fakeProfiles) Put(core.Key, core.Observation) {}
