package guest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/trace"
)

// A directory a step looked at is recorded as a listing, not only as a read.
//
// Κ₂ has 𝐷 - "directories listed, with the digest of each listing" - for exactly
// this, `Observation.Listings` carries it, `absorb` folds it, `observationPage`
// pages it, the profile store keeps it and the consistency check verifies it.
// Nothing ever filled it: `recordSightings` called `w.read` for every path it
// was given, and `observation()` returned `Listings` as a fresh empty map.
//
// The consequence is a false L2 hit, which is I3 - the one failure this design
// exists to prevent. Measured: `COPY ctx /c` followed by `RUN find /c`, build,
// add `ctx/b.txt`, rebuild. The context is re-digested and the COPY re-runs, and
// then the RUN takes an L2 hit and hands back the old listing. `ls` and a shell
// glob do the same, because all three enumerate rather than read. Earthly gets
// all three right.
//
// It is invisible to `usableObservation`, which is the guard meant to catch a
// lossy source: the observation is not empty - the step read /bin/sh and its
// libraries - and `Incomplete` is false, because the tracer does not know it
// missed anything. A source that cannot report its own loss cannot be used for
// cache keys, and this one could not report this loss.
func TestADirectoryIsRecordedAsAListing(t *testing.T) {
	t.Parallel()

	s, h := copyFixture(t)

	dir := "/w"

	err := os.WriteFile(filepath.Join(h.Root(), "w", "seen.txt"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	s.recordSightings(h, h.Root(), trace.Sightings{
		Paths:  []string{dir},
		Opened: []string{dir},
	}, nil, nil)

	obs := s.observationOf(h)

	if _, ok := obs.Listings[dir]; !ok {
		t.Errorf("%q is a directory the step opened and no listing was"+
			" recorded: %v\n  a step that enumerates it keys on nothing that"+
			" changes when its contents do", dir, obs.Listings)
	}
}

// A directory the step only walked past keeps no listing.
//
// Opening a directory is how a step enumerates it. Stat'ing one is how it
// resolves a path to a file inside, which every step does to every ancestor of
// everything it reads - so recording a listing for those would key `RUN cat
// /c/f.txt` on the whole of `/c`, and a sibling file appearing would re-run a
// step that never looked at it.
//
// Measured before this narrowing: `COPY --dir ctx /c` with `RUN cat /c/f.txt`,
// then a new `ctx/sibling.txt`, re-ran the RUN. It is the cost the first version
// of the fix accepted deliberately, and the tracer turned out to have kept
// enough to avoid paying it.
func TestADirectoryOnlyWalkedThroughKeepsNoListing(t *testing.T) {
	t.Parallel()

	s, h := copyFixture(t)

	dir := "/w"

	err := os.WriteFile(filepath.Join(h.Root(), "w", "seen.txt"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// Seen, but not opened: the step stat'ed it on the way to something inside.
	s.recordSightings(h, h.Root(), trace.Sightings{Paths: []string{dir}}, nil, nil)

	obs := s.observationOf(h)

	if _, ok := obs.Listings[dir]; ok {
		t.Errorf("%q was only interrogated and a listing was recorded anyway:"+
			" a file appearing beside the one the step read would re-run it",
			dir)
	}

	// Still a read: the step did ask about the directory, and its mode and
	// ownership are as much an input as any file's.
	if _, ok := obs.Reads[dir]; !ok {
		t.Errorf("%q was not recorded at all", dir)
	}
}

// The read digest of a directory cannot stand in for its listing.
//
// This is the mechanism, stated so the next reader does not have to rediscover
// it: `PathDigestIn` digests the entry *at* the path - for a directory its own
// mode and ownership - which is the right answer to the question it is asked and
// the wrong one for "what is in here". Adding a file changes what the directory
// contains and not what the directory *is*, so the read digest is identical
// across the two, and a key built from reads alone cannot tell them apart.
func TestADirectorysReadDigestDoesNotSeeItsContents(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	before, err := layer.PathDigestIn(dir, layer.IDMap{}, layer.IDMap{})
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	after, err := layer.PathDigestIn(dir, layer.IDMap{}, layer.IDMap{})
	if err != nil {
		t.Fatal(err)
	}

	if before != after {
		t.Skip("the read digest of a directory now covers its contents," +
			" so the listing is no longer the only thing that can see them")
	}

	// Not a defect in PathDigestIn - it answers what is *at* the path. The
	// defect was keying a directory on it alone.
	t.Logf("a directory's read digest is unchanged by a file appearing in it"+
		" (%s), which is why 𝐷 exists", before)
}
