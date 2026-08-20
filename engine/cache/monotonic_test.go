package cache_test

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cache"
	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

func openCache(t *testing.T) *cache.Cache {
	t.Helper()

	c, err := cache.Open(filepath.Join(t.TempDir(), "cache"))
	if err != nil {
		t.Fatal(err)
	}

	return c
}

// An entry that exists is never changed. This is I9, and it was not true.
//
// Green paper 2.4: σ evolves by insertion and removal only, no entry is ever
// modified in place, "which is what lets a concurrent reader hold a digest and
// be certain the bytes behind it will not change under them". The blob store
// says so in its own comments and behaves that way - it stats the destination
// and returns early. The action cache renamed straight over whatever was there.
//
// §5.1 records I9's enforcement as "store panics on rewrite of an existing key"
// and its test as **[GAP]**. One half of the store did what the row claims and
// the other half did the opposite, and nothing anywhere asked.
//
// The cost is not an abstraction. Κ₂ hashes the operation, the environment and
// the platform along with what the step observed (4.6), so two entries under
// one key naming *different* layers is a step that read the same things and
// produced different output - an I1 violation, the exact thing §6's screening
// exists to catch. Overwriting is how it gets laundered: the second build wins
// and there is no longer any evidence there was a first.
func TestAnExistingEntryIsNotOverwritten(t *testing.T) {
	t.Parallel()

	c := openCache(t)
	k := key(0x11)

	first := core.Entry{Layer: ir.NodeID{0xa1}, Bytes: 100, Writer: testFirst}
	second := core.Entry{Layer: ir.NodeID{0xb2}, Bytes: 200, Writer: testSecond}

	c.Put(k, first)
	c.Put(k, second)

	got, ok := c.Get(k)
	if !ok {
		t.Fatal("the entry disappeared")
	}

	if got.Layer != first.Layer {
		t.Errorf("the entry was modified in place: held %s, now %s", first.Layer, got.Layer)
	}
}

// Putting the same claim twice is an insertion that was already made.
//
// The common case by a mile: two identical builds, or the same target reached
// twice in one graph. It must not be an error and must not be recorded as a
// disagreement, or the conflict report becomes noise and stops being read -
// which is the usual way a warning dies.
func TestPuttingTheSameClaimTwiceIsNotAConflict(t *testing.T) {
	t.Parallel()

	c := openCache(t)
	k := key(0x22)
	e := entry(0xc3)

	c.Put(k, e)
	c.Put(k, e)

	got, ok := c.Get(k)
	if !ok || got.Layer != e.Layer {
		t.Fatalf("the entry did not survive being re-put: %+v %v", got, ok)
	}

	if n := c.ConflictCount(); n != 0 {
		t.Errorf("re-putting an identical claim was recorded as %d conflict(s)", n)
	}
}

// A refused rewrite is reported, because the refusal is the interesting part.
//
// Silently keeping the first entry would honour I9 and lose exactly what the
// violation was worth knowing: that a key which is supposed to determine a
// result did not. **Refusing without reporting turns a determinism bug into a
// cache miss nobody can explain.**
func TestAConflictingPutIsRecorded(t *testing.T) {
	t.Parallel()

	c := openCache(t)
	k := key(0x33)

	held := ir.NodeID{0xd4}
	given := ir.NodeID{0xe5}

	c.Put(k, core.Entry{Layer: held, Bytes: 1, Writer: testFirst})
	c.Put(k, core.Entry{Layer: given, Bytes: 2, Writer: testSecond})

	if n := c.ConflictCount(); n != 1 {
		t.Fatalf("a rewrite with a different layer was recorded as %d conflict(s)", n)
	}

	conflicts := c.Conflicts()
	if len(conflicts) != 1 {
		t.Fatalf("expected one recorded conflict, got %d", len(conflicts))
	}

	if got := conflicts[0]; got.Held != held || got.Given != given || got.Key != k {
		t.Errorf("the conflict names the wrong thing: %+v", got)
	}
}

// The recorded conflicts come back in the same order every time (I12).
//
// A build's report is part of what it produces, and one that varies between
// runs makes every tool that diffs two builds report noise. Three times in one
// session a map's iteration order reached this engine's output; a slice
// appended to from parallel steps is the same hazard wearing a different hat.
func TestConflictsAreReportedInAStableOrder(t *testing.T) {
	t.Parallel()

	c := openCache(t)

	var wg sync.WaitGroup

	// Appended from parallel steps, which is how they arrive in a real build.
	for _, seed := range []byte{0x44, 0x45, 0x46, 0x47} {
		wg.Add(1)

		go func() {
			defer wg.Done()

			k := key(seed)
			c.Put(k, core.Entry{Layer: ir.NodeID{0xf0}, Bytes: 1, Writer: testFirst})
			c.Put(k, core.Entry{Layer: ir.NodeID{0xf1}, Bytes: 2, Writer: testSecond})
		}()
	}

	wg.Wait()

	first := ""

	for range 8 {
		var b strings.Builder

		for _, x := range c.Conflicts() {
			b.WriteString(x.Key.String())
		}

		if first == "" {
			first = b.String()

			continue
		}

		if b.String() != first {
			t.Fatalf("two reads of the conflict list disagreed:\n  %s\n  %s", first, b.String())
		}
	}
}

// Concurrent writers of ONE key never leave an entry nobody can read.
//
// `TestConcurrentWritersDoNotCorrupt` looks like it covers this and covers the
// opposite: it uses thirty-two distinct keys, so no two writers ever touch the
// same file and the only shared thing under test is the directory. **A
// concurrency test whose workers do not contend tests the absence of the
// hazard.**
//
// The temporary file was named `<key>.<pid>.tmp`, and the comment said the pid
// "keeps concurrent builds from sharing a temporary file" - which it does, and
// which says nothing about concurrent *steps of one build*. This scheduler runs
// steps in parallel and two of them can share a key: the same target reached
// twice, or two steps whose observations coincide under Κ₂.
//
// Two goroutines then open one path with O_TRUNC and write claims of different
// lengths, and the loser's tail survives past the winner's end. Get treats
// unreadable as a miss, so the damage is lost work rather than a wrong answer -
// which is why it could sit there indefinitely with nobody noticing.
func TestConcurrentPutsOfOneKeyLeaveAReadableEntry(t *testing.T) {
	t.Parallel()

	for range 40 {
		c := openCache(t)
		k := key(0x55)

		var wg sync.WaitGroup

		for i := range 8 {
			wg.Add(1)

			go func() {
				defer wg.Done()

				// Deliberately different lengths: identical payloads overwrite
				// each other harmlessly and the race stays invisible.
				c.Put(k, core.Entry{
					Layer:  ir.NodeID{byte(0x60 + i)},
					Bytes:  int64(1) << (8 * i),
					Writer: strings.Repeat("w", i+1),
				})
			}()
		}

		wg.Wait()

		if _, ok := c.Get(k); !ok {
			t.Fatal("concurrent writers left an entry that cannot be read")
		}
	}
}
