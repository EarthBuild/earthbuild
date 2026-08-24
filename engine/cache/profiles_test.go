package cache_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cache"
	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

func classOf(n int) core.Key {
	return core.StepClass(&ir.Node{
		Op: ir.Op{Kind: ir.OpExec, Args: []string{"cc", fmt.Sprintf("f%d", n)}},
	})
}

func sample() core.Observation {
	return core.Observation{
		Reads:    map[string]ir.NodeID{"/usr/include/stdio.h": {1}, "/src/main.c": {2}},
		Negative: []string{"/opt/include/stdio.h"},
		Listings: map[string]ir.NodeID{"/usr/include": {3}},
	}
}

// A profile written by one build is read by the next.
//
// `Profiles` is the other half of the L2 path. `Views` was implemented in E114
// and this was still a map in a test file, so `cli.go` set neither and the whole
// tier was unreachable - profiles, `Consistent`, Κ₂.
//
// A profile is a *prediction*, not a result: it may be absent, stale or wrong,
// and `Consistent` is what makes acting on one safe. That is the whole design of
// this store - every failure below degrades to "no prediction", because a build
// that cannot read its hints is a slower build and a build that trusts a bad one
// is a wrong build.
func TestAProfileSurvivesToTheNextBuild(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	first, err := cache.OpenProfiles(dir)
	if err != nil {
		t.Fatal(err)
	}

	first.Put(classOf(1), sample())

	next, err := cache.OpenProfiles(dir)
	if err != nil {
		t.Fatal(err)
	}

	got, ok := next.Get(classOf(1))
	if !ok {
		t.Fatal("the profile did not survive: the tier can never hit")
	}

	want := sample()

	if len(got.Reads) != len(want.Reads) || got.Reads["/src/main.c"] != want.Reads["/src/main.c"] {
		t.Errorf("the reads came back as %v", got.Reads)
	}

	if len(got.Negative) != 1 || got.Negative[0] != want.Negative[0] {
		t.Errorf("the negative lookups came back as %v", got.Negative)
	}

	if got.Listings["/usr/include"] != want.Listings["/usr/include"] {
		t.Errorf("the listings came back as %v", got.Listings)
	}

	// The round trip must preserve the *key*, not merely the paths: a profile
	// that comes back deriving a different Κ₂ is a profile that can never name
	// an entry, and the tier would look implemented and never hit.
	n := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"cc", "a"}}}
	if core.DeriveObservedKey(n, nil, got) != core.DeriveObservedKey(n, nil, want) {
		t.Error("the profile round-tripped to a different observed key")
	}
}

// An incomplete observation is never stored.
//
// The scheduler already refuses to key one, so this is defence in depth - and
// it is the kind that has earned its place five times this session: a rule
// applied at one of the two places it holds. A store that accepted an
// incomplete profile would hand it to `tryL2` on the next build, where nothing
// remembers where it came from.
func TestAnIncompleteObservationIsNotStored(t *testing.T) {
	t.Parallel()

	p, err := cache.OpenProfiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	obs := sample()
	obs.Incomplete = true

	p.Put(classOf(2), obs)

	if _, ok := p.Get(classOf(2)); ok {
		t.Error("a profile the source admitted was lossy was stored," +
			" and the next build has no way to know that")
	}
}

// A profile that cannot be read is not a profile.
//
// Truncated by a full disk, half-written by a kill, corrupted by anything: the
// answer is "no prediction", never an error and never a partial observation. A
// partial one is the dangerous case - it names fewer paths than the step read,
// which is exactly the false-hit shape I3 exists to prevent - so a file that
// does not parse whole is discarded whole.
func TestAnUnreadableProfileIsAMiss(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	p, err := cache.OpenProfiles(dir)
	if err != nil {
		t.Fatal(err)
	}

	p.Put(classOf(3), sample())

	entries, err := os.ReadDir(filepath.Join(dir, "profiles"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("nothing was written: %v", err)
	}

	victim := filepath.Join(dir, "profiles", entries[0].Name())

	err = os.WriteFile(victim, []byte(`{"reads":{"/a":`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	next, err := cache.OpenProfiles(dir)
	if err != nil {
		t.Fatalf("a corrupt profile stopped the store from opening: %v", err)
	}

	if _, ok := next.Get(classOf(3)); ok {
		t.Error("a half-written profile was returned as a prediction")
	}
}

// Concurrent writers do not produce a torn profile.
//
// Steps run in parallel and several of one class finish at once. A reader that
// caught a half-written file would get a *subset* of the paths - which parses,
// looks complete, and is the false-hit shape again. Written to a temporary name
// and renamed, so a reader sees the old file or the new one.
func TestConcurrentWritersLeaveAReadableProfile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	p, err := cache.OpenProfiles(dir)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup

	for range 16 {
		wg.Go(func() {
			p.Put(classOf(4), sample())
		})
	}

	wg.Wait()

	got, ok := p.Get(classOf(4))
	if !ok {
		t.Fatal("nothing readable survived sixteen concurrent writers")
	}

	if len(got.Reads) != len(sample().Reads) {
		t.Errorf("a torn profile was readable: %v", got.Reads)
	}
}

// Two machines that learned the same thing hold the same bytes.
//
// The same property the prediction history has, and for the same reason: a
// store whose bytes depend on map iteration order cannot be compared, shared or
// diffed, and at the fleet (Appendix C) it is what turns "we both learned this"
// into two entries.
func TestAProfileIsWrittenDeterministically(t *testing.T) {
	t.Parallel()

	read := func() []byte {
		t.Helper()

		dir := t.TempDir()

		p, err := cache.OpenProfiles(dir)
		if err != nil {
			t.Fatal(err)
		}

		p.Put(classOf(5), sample())

		entries, err := os.ReadDir(filepath.Join(dir, "profiles"))
		if err != nil || len(entries) != 1 {
			t.Fatalf("expected one profile: %v", err)
		}

		// A file this test just wrote, under a directory it made (gosec G304).
		b, err := os.ReadFile(filepath.Join(dir, "profiles", entries[0].Name())) //nolint:gosec // our own temp dir
		if err != nil {
			t.Fatal(err)
		}

		return b
	}

	if a, b := read(), read(); string(a) != string(b) {
		t.Errorf("two writes of one observation differ:\n  %s\n  %s", a, b)
	}
}
