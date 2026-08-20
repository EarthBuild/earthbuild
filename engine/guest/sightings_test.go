package guest

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/trace"
)

// A step's sightings become reads and absences, digested in the mount.
//
// The tracer says only that a path was *named* - the answer is sent before the
// syscall runs, so nothing about how it came out is knowable there. Which of
// them existed is decided here, against the step's own filesystem, exactly as it
// is for a copy's destination.
//
// The split is not cosmetic. A path that was there is a read and keys the step
// on its contents; a path that was not is a **negative lookup**, and the step
// behaved as it did *because* nothing was there. A base where that file exists
// would build differently, so recording an absence as merely "not read" is the
// false hit I3 forbids - and the loader's search for `glibc-hwcaps/x86-64-v3`
// is that case in every dynamically linked step there is (E210).
func TestSightingsBecomeReadsAndAbsences(t *testing.T) {
	t.Parallel()

	s, h := copyFixture(t)

	present := "/w/seen.txt"

	err := os.WriteFile(filepath.Join(h.Root(), "w", "seen.txt"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	absent := "/w/never-existed-6b1d.txt"

	s.recordSightings(h, h.Root(), trace.Sightings{Paths: []string{present, absent}}, nil)

	obs := s.observationOf(h)

	if _, ok := obs.Reads[present]; !ok {
		t.Errorf("%q is in the mount and was not recorded as a read: %v",
			present, obs.Reads)
	}

	if !slices.Contains(obs.Negative, absent) {
		t.Errorf("%q is not in the mount and was not recorded as an absence:"+
			" %v\n  a base where it exists would build differently",
			absent, obs.Negative)
	}

	if _, ok := obs.Reads[absent]; ok {
		t.Errorf("%q was recorded as a read of something that is not there",
			absent)
	}

	if obs.Incomplete {
		t.Error("an observation of two paths, both resolved, reports itself" +
			" incomplete")
	}
}

// An incomplete trace stays incomplete after the paths are digested.
//
// The tracer declares a gap - a call in another architecture's numbering, a path
// it could not read - and digesting the paths it *did* get must not wash that
// out. A source that launders its own gaps by resolving the rest is exactly what
// `Incomplete` exists to prevent (§3.4).
func TestAnIncompleteTraceStaysIncomplete(t *testing.T) {
	t.Parallel()

	s, h := copyFixture(t)

	err := os.WriteFile(filepath.Join(h.Root(), "w", "seen.txt"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	s.recordSightings(h, h.Root(), trace.Sightings{
		Paths:      []string{"/w/seen.txt"},
		Incomplete: true,
		Why:        []string{"a syscall in another architecture's numbering"},
	}, nil)

	obs := s.observationOf(h)

	if !obs.Incomplete {
		t.Error("a declared gap was lost once the paths it came with were" +
			" resolved; the observation now claims to be complete")
	}

	if _, ok := obs.Reads["/w/seen.txt"]; !ok {
		t.Error("the paths that were readable are gone too; an incomplete" +
			" observation is still worth what it saw")
	}
}

// A step that ran without a tracer is not a step that read nothing.
//
// The distinction is the whole of the tier's safety. An empty observation
// reported as complete says the step read nothing at all, which matches every
// base - so an untraced step would serve L2 hits against filesystems it has
// never seen. `trace.Unobserved` is the most lossy a source gets, and it says so.
func TestAnUntracedStepIsIncompleteRatherThanEmpty(t *testing.T) {
	t.Parallel()

	s, h := copyFixture(t)

	s.recordSightings(h, h.Root(), trace.Unobserved(nil), nil)

	obs := s.observationOf(h)

	if !obs.Incomplete {
		t.Error("a step that ran with no tracer reports a complete" +
			" observation of nothing, which matches every base")
	}
}

// A path this engine mounted is not part of the step's base.
//
// `/etc/resolv.conf` is bound in so a step can resolve a hostname; `/proc` and
// `/dev` are the runtime's; a cache mount is a place the step is given to keep
// things between builds. **None of them come from the base**, and every one is
// regenerated or shared, so a step that reads one is stale on every later build
// whatever it actually looked at.
//
// Measured before it was fixed: `1 of 2 predictions stale (/etc/resolv.conf
// changed in the base)`, on every corpus target whose steps fetch anything -
// which is every package manager there is (E221, E222).
//
// The exclusion is by prefix, because a mount covers what is under it: a step
// reading `/proc/self/status` has read the runtime's `/proc`, and a cache mount
// at `/root/.cache` covers everything inside it.
func TestAPathTheEngineMountedIsNotAnInput(t *testing.T) {
	t.Parallel()

	s, h := copyFixture(t)

	err := os.WriteFile(filepath.Join(h.Root(), "w", "real.txt"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	provided := []string{"/etc/resolv.conf", "/proc", "/root/.cache"}

	s.recordSightings(h, h.Root(), trace.Sightings{Paths: []string{
		"/w/real.txt",
		"/etc/resolv.conf",
		"/proc/self/status",
		"/root/.cache/go-build/aa/bb",
	}}, provided)

	obs := s.observationOf(h)

	for _, p := range []string{"/etc/resolv.conf", "/proc/self/status", "/root/.cache/go-build/aa/bb"} {
		if _, ok := obs.Reads[p]; ok {
			t.Errorf("%q was mounted by this engine and recorded as a read of"+
				" the base", p)
		}

		if slices.Contains(obs.Negative, p) {
			t.Errorf("%q was mounted by this engine and recorded as an absence"+
				" in the base", p)
		}
	}

	if _, ok := obs.Reads["/w/real.txt"]; !ok {
		t.Errorf("an ordinary read was dropped along with the mounts: %v",
			obs.Reads)
	}

	if obs.Incomplete {
		t.Error("excluding a mounted path declared the observation incomplete;" +
			" nothing was lost - those paths are not the base's to describe")
	}
}

// A name that merely starts like a mount point is not under it.
//
// `/etc/resolv.conf.bak` is a file in the base, and excluding it because the
// string begins with a mount's would be a prefix match pretending to be a path
// match. The bug this prevents is silent in the safe direction - a lost read is
// a miss - which is exactly why it would never be found.
func TestASiblingOfAMountPointIsStillAnInput(t *testing.T) {
	t.Parallel()

	s, h := copyFixture(t)

	err := os.WriteFile(filepath.Join(h.Root(), "w", "resolv.conf.bak"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	s.recordSightings(h, h.Root(), trace.Sightings{
		Paths: []string{"/w/resolv.conf.bak"},
	}, []string{"/w/resolv.conf"})

	if _, ok := s.observationOf(h).Reads["/w/resolv.conf.bak"]; !ok {
		t.Error("a file whose name starts with a mount point's was excluded;" +
			" the match is on path components, not on characters")
	}
}
