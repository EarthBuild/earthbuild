package exec

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A fault-in is answered against the base it named.
//
// The host end of E303. A worker runs several steps at once, each with a primed
// base of its own, and a fault-in says which - so this looks up *that* step's
// stack and *that* step's directory, rather than guessing (E304).
func TestAFaultInIsAnsweredAgainstTheBaseItNamed(t *testing.T) {
	t.Parallel()

	one := t.TempDir()
	two := t.TempDir()

	var asked []string

	e := &Executor{
		Prime: func(_ context.Context, stack []ir.NodeID, _ []string, into string) error {
			asked = append(asked, into)

			return nil
		},
	}

	e.remember("h1", primedBase{stack: []ir.NodeID{{1}}, into: one})
	e.remember("h2", primedBase{stack: []ir.NodeID{{2}}, into: two})

	var (
		filled []ir.NodeID
		where  []string
	)

	e.Fetch = func(_ context.Context, stack []ir.NodeID, _, path string) error {
		filled = append(filled, stack...)
		where = append(where, path)

		return nil
	}

	err := e.FillFor(context.Background(), "h2", "/usr/bin/cc")
	if err != nil {
		t.Fatalf("%v", err)
	}

	if len(filled) != 1 || filled[0] != (ir.NodeID{2}) {
		t.Errorf("fetched from %v; the fault-in named h2's base", filled)
	}

	// And into h2's directory, not h1's: the path the guest named is relative
	// to the base it is running against.
	if len(where) != 1 || where[0] != filepath.Join(two, "usr/bin/cc") {
		t.Errorf("placed at %v, want under %s", where, two)
	}

	_ = asked
}

// A fault-in for a base nobody primed is refused.
//
// **Not answered from some other base**, and not silently succeeded. A handle
// this executor does not know is a step it did not prime, and fetching for it
// would be fetching from a stack chosen by accident (E304).
func TestAFaultInForAnUnknownBaseIsRefused(t *testing.T) {
	t.Parallel()

	e := &Executor{}

	e.Fetch = func(context.Context, []ir.NodeID, string, string) error {
		return errShouldNotAsk
	}

	err := e.FillFor(context.Background(), "nobody", "/usr/bin/cc")
	if err == nil {
		t.Fatal("a fault-in for a base this executor never primed was answered")
	}
}

// A base that has been released is forgotten.
//
// A step's primed directory is removed when it finishes, and a fault-in arriving
// afterwards is for a filesystem that no longer exists. Remembering it would be
// a map that grows for the life of the process, and answering from it would be
// writing into a directory somebody deleted.
func TestAReleasedBaseIsForgotten(t *testing.T) {
	t.Parallel()

	e := &Executor{}
	e.remember("h1", primedBase{stack: []ir.NodeID{{1}}, into: t.TempDir()})
	e.forget("h1")

	e.Fetch = func(context.Context, []ir.NodeID, string, string) error { return nil }

	err := e.FillFor(context.Background(), "h1", "/x")
	if err == nil {
		t.Error("a released base still answers fault-ins")
	}
}

var errShouldNotAsk = errors.New("this should not have been asked")
