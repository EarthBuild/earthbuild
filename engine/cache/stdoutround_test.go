package cache_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cache"
	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// What a step printed survives being written down and read back.
//
// **The whole point of keeping it.** A hit reproduces a step's effects; this is
// what lets it reproduce the step's observations too, which is what `LET
// v=$(cmd)` needs and has never had.
func TestWhatAStepPrintedSurvivesTheCache(t *testing.T) {
	t.Parallel()

	c, err := cache.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	key := core.Key{1}
	want := core.Entry{
		Layer: ir.NodeID{2}, Stdout: "three\nfiles\n", StdoutWhole: true,
	}

	c.Put(key, want)

	got, ok := c.Get(key)
	if !ok {
		t.Fatal("the entry was not found")
	}

	if got.Stdout != want.Stdout {
		t.Errorf("read back %q, wrote %q", got.Stdout, want.Stdout)
	}

	if !got.StdoutWhole {
		t.Error("a whole output came back as not whole, so a caller would" +
			"\n  re-run a command whose answer it already had")
	}
}

// An entry from before this existed says it has nothing whole.
//
// **Absent must stay absent.** An old entry did not record what its step
// printed, and reading that as "the step printed nothing, and that is all of
// it" would have a substitution evaluate to the empty string - silently, an
// empty string being a value and not an error, which is exactly the bug this
// field exists to end.
func TestAnEntryFromBeforeThisSaysItHasNothing(t *testing.T) {
	t.Parallel()

	c, err := cache.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	key := core.Key{3}
	c.Put(key, core.Entry{Layer: ir.NodeID{4}})

	got, ok := c.Get(key)
	if !ok {
		t.Fatal("the entry was not found")
	}

	if got.StdoutWhole {
		t.Error("an entry that recorded no output claims to have all of it")
	}
}
