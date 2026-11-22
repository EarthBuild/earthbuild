package guest

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A copy reports what it looked at in the base.
//
// **The first real observation source in this engine**, and it needs no tracing
// mechanism at all: the guest performs a copy's reads itself, so it can say
// what they were.
//
// What matters is *which* filesystem. `Consistent(pred, view)` checks a
// prediction against `Views.View(ctx, base)` - the step's **base**, not the
// stack it copies from. A copy's source layers reach the key already, through
// refs and `Op.Content`. So the observation a COPY makes is about its
// **destination**, and the claim it lets Κ₂ state is exact:
//
//	a COPY over a different base produces the same layer
//	iff the destination looked the same
//
// Which is a common and currently expensive miss. Bump a base image and every
// `COPY` above it rebuilds, because the chain key includes the base - even
// though a copy of an unchanged file into an unchanged destination cannot
// produce anything different.
//
// The destination's *kind* is what is looked at, and only that. `COPY x /app/`
// places inside a directory and renames onto anything else; `COPY --dir tree
// /placed` gives /placed/tree when /placed exists and /placed itself when it
// does not. Contents do not enter it: what the step produces is the delta it
// writes, which is the same whatever was underneath.
func TestACopyObservesItsDestination(t *testing.T) {
	t.Parallel()

	t.Run("an existing destination directory is a read", func(t *testing.T) {
		t.Parallel()

		s, h := copyFixture(t)

		err := s.copyIn(h, []string{testSrcLayer}, "src.txt", "/w/", copyOpts{})
		if err != nil {
			t.Fatal(err)
		}

		obs := s.observationOf(h)

		if _, ok := obs.Reads["/w"]; !ok {
			t.Errorf("the destination /w was not recorded as read: %v", obs.Reads)
		}

		if obs.Incomplete {
			t.Error("the copy reported its own observation as lossy")
		}
	})

	// The mirror, and the one that makes Κ₂ safe here. A destination that does
	// not exist is a *negative* lookup: the copy behaved as it did **because**
	// nothing was there, and a base where something is there would produce a
	// different layer. Recording only reads would let this hit against that
	// base - 𝑁 is not a refinement of 𝑅 (green paper 3.4, I3).
	t.Run("an absent destination is a negative lookup", func(t *testing.T) {
		t.Parallel()

		s, h := copyFixture(t)

		err := s.copyIn(h, []string{testSrcLayer}, "src.txt", "/nowhere/there.txt", copyOpts{})
		if err != nil {
			t.Fatal(err)
		}

		obs := s.observationOf(h)

		if !slices.Contains(obs.Negative, "/nowhere") {
			t.Errorf("the absent destination directory was not recorded as a"+
				" negative lookup, so a base that has one would satisfy this"+
				" prediction: %v", obs.Negative)
		}

		if _, ok := obs.Reads["/nowhere"]; ok {
			t.Error("a path that was not there was recorded as read")
		}
	})

	// A file where a directory would have been is a different result, so it has
	// to be a different observation. Both are "the destination exists", and the
	// digest tells them apart because it carries the mode (E114).
	t.Run("a destination file and a destination directory differ", func(t *testing.T) {
		t.Parallel()

		asDir, asFile := copyTwoWays(t)

		if asDir == asFile {
			t.Error("a destination that is a directory and one that is a file" +
				" observe identically, so a copy that placed a file inside one" +
				" would be reused where it would have renamed onto the other")
		}
	})

	t.Run("nothing outside the destination is claimed", func(t *testing.T) {
		t.Parallel()

		s, h := copyFixture(t)

		err := s.copyIn(h, []string{testSrcLayer}, "src.txt", "/w/", copyOpts{})
		if err != nil {
			t.Fatal(err)
		}

		// The source layer is not part of the base. Recording a path from it
		// would make the prediction unverifiable - `Views.View` is built from
		// the base stack and has never heard of it - so the tier would look
		// implemented and never hit.
		for path := range s.observationOf(h).Reads {
			if path == "/src.txt" || path == "src.txt" {
				t.Errorf("a path from the copy's *source* was recorded as a"+
					" read of the base: %v", path)
			}
		}
	})
}

// copyTwoWays returns the destination digest observed when the destination is a
// directory and when it is a file.
func copyTwoWays(t *testing.T) (asDir, asFile string) {
	t.Helper()

	run := func(makeFile bool) string {
		t.Helper()

		s, h := copyFixture(t)

		dest := "/dest"
		p := filepath.Join(h.root, "dest")

		var err error
		if makeFile {
			err = os.WriteFile(p, []byte("x\n"), 0o600)
		} else {
			err = os.MkdirAll(p, 0o750)
		}

		if err != nil {
			t.Fatal(err)
		}

		err = s.copyIn(h, []string{testSrcLayer}, "src.txt", dest, copyOpts{})
		if err != nil {
			t.Fatal(err)
		}

		id, ok := s.observationOf(h).Reads["/dest"]
		if !ok {
			t.Fatalf("the destination was not observed (file=%v)", makeFile)
		}

		return id.String()
	}

	return run(false), run(true)
}

// A destination reached through a symlink is admitted as lossy.
//
// `within` is a lexical join, so the chain walked is exactly the components the
// step named, and each component's digest distinguishes a symlink from a
// directory. What it does *not* cover is where the symlink points: two bases
// where `/link -> /a` and `/link -> /b` observe identically at `/link`, and the
// copy lands somewhere different in each.
//
// The precise fix is to follow and observe the target as well. The honest one,
// today, is `Incomplete` - which is exactly what the field is for. **A source
// that admits a gap costs an L2 hit; one that hides it costs correctness**
// (green paper §3.4), and a rare case handled conservatively is worth more than
// a common case handled optimistically.
func TestACopyThroughASymlinkAdmitsItIsLossy(t *testing.T) {
	t.Parallel()

	s, h := copyFixture(t)

	err := os.Symlink("w", filepath.Join(h.root, "link"))
	if err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	err = s.copyIn(h, []string{testSrcLayer}, "src.txt", "/link/", copyOpts{})
	if err != nil {
		t.Fatal(err)
	}

	if !s.observationOf(h).Incomplete {
		t.Error("a copy whose destination path went through a symlink reported" +
			" a complete observation, but nothing recorded where the link pointed:" +
			"\n  two bases whose link targets differ would satisfy one prediction")
	}
}

// The ordinary case is not marked lossy.
//
// The companion, because "declare lossy" is satisfiable by declaring everything
// lossy - and then the source is honest, useless, and indistinguishable from
// not having been written.
func TestAnOrdinaryCopyIsNotLossy(t *testing.T) {
	t.Parallel()

	s, h := copyFixture(t)

	err := s.copyIn(h, []string{testSrcLayer}, "src.txt", "/w/", copyOpts{})
	if err != nil {
		t.Fatal(err)
	}

	if s.observationOf(h).Incomplete {
		t.Error("a plain copy into a plain directory declared itself lossy," +
			" so no copy could ever produce a usable observation")
	}
}

// The root is not part of what a copy observed.
//
// The chain walk stopped at `/`, and `/` is the one component whose existence
// is never in question: a copy's destination decides where its source lands,
// and "the filesystem has a root" decides nothing. What it *does* have is a
// digest - mode, ownership, extended attributes - that differs between two base
// images for reasons no copy depends on.
//
// So including it made every copy's prediction stale the moment the base moved,
// which is precisely the case the tier exists for. Measured on a real bump from
// `alpine:3.21` to `alpine:3.22`:
//
//	cache   1 hit, 3 miss, 1 of 3 predictions stale
//
// The tier ran, checked, and refused - **L2 never hitting and nothing ever
// being wrong**, which is the failure mode E121 named and then tested for
// everything except the root.
func TestACopyDoesNotObserveTheRoot(t *testing.T) {
	t.Parallel()

	s, h := copyFixture(t)

	err := s.copyIn(h, []string{testSrcLayer}, "src.txt", "/w/", copyOpts{})
	if err != nil {
		t.Fatal(err)
	}

	obs := s.observationOf(h)

	if _, ok := obs.Reads["/"]; ok {
		t.Error("the root was recorded as read, so any base whose root differs" +
			" - which is any two base images - makes this prediction stale")
	}

	for _, p := range obs.Negative {
		if p == "/" {
			t.Error("the root was recorded as a negative lookup, which is a claim" +
				" that the filesystem has no root")
		}
	}

	// And the destination itself is still there, or this has been fixed by
	// observing nothing at all.
	if _, ok := obs.Reads["/w"]; !ok {
		t.Errorf("the destination is no longer observed: %v", obs.Reads)
	}
}
