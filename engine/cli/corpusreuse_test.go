package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// How much of a real corpus the observed-input tier actually reuses.
//
// `TestCorpusTargetsActuallyBuild` answers "does it run". This answers the
// question S5 was built for: when a base moves in a way the steps did not look
// at, **how many of them come back from Κ₂** rather than being rebuilt.
//
// A measurement, like its neighbour, and reported rather than asserted against a
// number. A corpus of other people's Earthfiles is not a fixture and the honest
// output is a table somebody reads.
//
// The perturbation is a line inserted after the first `FROM`:
//
//	RUN true # earth-perturb-<n>
//
// which moves the chain key of everything below it and changes nothing any step
// reads. That is the shape Κ₂ exists for, and it is the *only* generic
// perturbation available: bumping a base image tag changes the shell and the
// libc, so a step that reads them should miss and a hit would be the false hit
// I3 forbids (E217).
func TestHowMuchOfTheCorpusTheObservedTierReuses(t *testing.T) { //nolint:paralleltest // boots a sandbox
	if os.Getenv("EARTH_TEST_BUILD") == "" {
		t.Skip("set EARTH_TEST_BUILD=1 to build corpus targets rather than plan them")
	}

	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	// The guest, before `requireSandbox` asks whether there is one.
	//
	// This sweep did not build it, so on a machine that has not installed one it
	// attempted a target, failed with `cannot find earth-guestd`, measured
	// nothing and **passed** - "1 targets, 0 with at least one step reused, 0
	// such steps out of 0 attempted" (E496). The order is l2run's and for
	// l2run's reason: `Available()` looks for the binary, so asking first skips
	// on every machine that builds this from source.
	t.Setenv("EARTH_GUESTD", buildGuestd(t))

	requireSandbox(t)

	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))

	deadline := time.Now().Add(duration(t, "EARTH_TEST_REUSE_TIME", 10*time.Minute))
	limit := count(t, "EARTH_TEST_REUSE_MAX", 8)

	var attempted, reused, steps, l2 int

	// A substring filter, because a measurement nobody can point at a case is
	// hard to act on: chasing one target's staleness meant waiting for the eight
	// before it and then running out of clock.
	only := os.Getenv("EARTH_TEST_REUSE_ONLY")

	for _, b := range buildable(t) {
		if attempted >= limit || time.Now().After(deadline) {
			break
		}

		if only != "" && !strings.Contains(b.name(), only) {
			continue
		}

		dir, ok := stagedCopy(t, b)
		if !ok {
			continue
		}

		store := storeDir(t)

		first, err := buildIn(t, dir, store, b.target)
		if err != nil {
			// Not this test's business: whether a corpus target builds at all
			// is measured next door, and repeating the failure here would
			// double-count an environment problem as a cache result.
			continue
		}

		attempted++

		err = perturb(filepath.Join(dir, testEarthfile), attempted)
		if err != nil {
			t.Fatal(err)
		}

		second, err := buildIn(t, dir, store, b.target)
		if err != nil {
			t.Logf("%s: built once and not after the base moved: %v", b.name(), err)

			continue
		}

		// All three, because they are disjoint: `2 hit, 1 miss, 7 by observed
		// inputs` is ten steps, not three. Counting the denominator as hits plus
		// misses gave "15 of 9", which is the sort of number that says the
		// arithmetic is wrong rather than the engine.
		hits, misses := hitsAndMisses(second)
		observed := observedHits(second)

		steps += hits + misses + observed
		l2 += observed

		if observed > 0 {
			reused++
		}

		t.Logf("%-28s first: %s\n%-28s moved: %s",
			b.name(), cacheLine(first), "", cacheLine(second))
	}

	t.Logf("%d targets, %d with at least one step reused by observed inputs,"+
		" %d such steps out of %d attempted",
		attempted, reused, l2, steps)

	// A sweep that measured nothing **fails**.
	//
	// It skipped, and a skip is a pass at the package level: the run that found
	// this reported one target, zero steps and `ok`. The caller asked for a
	// measurement by setting two environment variables; answering "no targets"
	// quietly is the one outcome they cannot act on (E496).
	//
	// Steps rather than targets: a target that failed to build is still counted
	// as attempted, which is how one attempt and no steps read as a measurement
	// at all.
	if steps == 0 {
		t.Fatalf("%d target(s) attempted and no step ran, so nothing was"+
			" measured\n  a sweep that measures nothing is not a sweep that"+
			" found nothing", attempted)
	}
}

// stagedCopy puts a corpus target's directory somewhere writable.
//
// The Earthfile is edited between the two builds, and editing the repository's
// own corpus would leave it modified when the test fails.
func stagedCopy(t *testing.T, b buildTarget) (string, bool) {
	t.Helper()

	src := filepath.Dir(b.file)
	dst := t.TempDir()

	err := filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err //nolint:wrapcheck // walk's own error
		}

		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err //nolint:wrapcheck // as above
		}

		if fi.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o750) //nolint:wrapcheck // as above
		}

		body, err := os.ReadFile(p)
		if err != nil {
			return err //nolint:wrapcheck // as above
		}

		return os.WriteFile(filepath.Join(dst, rel), body, 0o600) //nolint:wrapcheck // as above
	})
	if err != nil {
		t.Logf("%s: could not be staged: %v", b.name(), err)

		return "", false
	}

	return dst, true
}

// perturb moves a build's base without changing anything a step reads.
func perturb(path string, n int) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	lines := strings.Split(string(body), "\n")

	for i, l := range lines {
		if !strings.HasPrefix(strings.TrimSpace(l), "FROM ") {
			continue
		}

		// The indentation of the line it follows, so a target-scoped FROM keeps
		// its block and a file-scoped one stays at the margin.
		indent := l[:len(l)-len(strings.TrimLeft(l, " \t"))]

		lines = append(lines[:i+1],
			append([]string{indent + "RUN true # earth-perturb-" + strconv.Itoa(n)},
				lines[i+1:]...)...)

		return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600) //nolint:wrapcheck // named above
	}

	return fmt.Errorf("%s has no FROM to perturb", path)
}

// buildIn runs one target and returns the log.
func buildIn(t *testing.T, dir, store, target string) (string, error) {
	t.Helper()

	t.Setenv(testCacheDirEnv, store)

	var log bytes.Buffer

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: target, Out: &log, Platform: testPlatform(),
	})

	return log.String(), err
}

var (
	hitsRe = regexp.MustCompile(`(\d+) hit, (\d+) miss`)
	l2Re   = regexp.MustCompile(`(\d+) by observed inputs`)
)

func hitsAndMisses(log string) (hits, misses int) {
	m := hitsRe.FindStringSubmatch(cacheLine(log))
	if m == nil {
		return 0, 0
	}

	h, _ := strconv.Atoi(m[1])
	s, _ := strconv.Atoi(m[2])

	return h, s
}

func observedHits(log string) int {
	m := l2Re.FindStringSubmatch(cacheLine(log))
	if m == nil {
		return 0
	}

	n, _ := strconv.Atoi(m[1])

	return n
}
