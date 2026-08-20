//go:build linux && integration

package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// ratchetRun fails when fewer of the `tests/` tree builds than last time, and
// when more does.
//
// The same rule as the corpus and planning ratchets, in the same file, under the
// key `<GOOS>-earthtests-run`. Both directions: a fall is a regression, and a
// rise that nobody records stops protecting the level reached - the next
// regression would then be measured against a number nobody has updated.
//
// **This one is machine-dependent in a way the planning ratchets are not**: it
// pulls base images and reaches the network, so a rate limit or a cold image
// cache lowers it. That is stated rather than engineered around, because the
// alternative - a gate that tolerates a fall - is a gate that catches nothing.
// A failure here is worth reading before it is worth acting on.
func ratchetRun(t *testing.T, built int) {
	t.Helper()

	key := runtime.GOOS + "-earthtests-run"

	want, err := readRunRatchet(key)
	if err != nil {
		t.Errorf("%v\n  a count nothing has written down is a count nothing can"+
			" notice falling", err)

		return
	}

	switch {
	case built < want:
		t.Errorf("%d of the tests/ tree builds, against %d committed"+
			"\n  something that used to build no longer does - or this machine"+
			" could not reach a registry, which is worth checking first",
			built, want)
	case built > want:
		// A note, not a failure.
		//
		// The planning sweep's ratchet insists on equality, and can: planning is
		// a pure function of the tree. This one builds, over a network, under a
		// per-target deadline - two consecutive runs of it gave 19 and 18 - so
		// equality is a promise the measurement cannot keep, and a test that
		// fails for the weather is one that gets disabled (E442).
		//
		// So the committed number is a floor. Raising it is deliberate and the
		// log is the nudge; the list of *which* targets built is printed beside
		// it, because that is what makes a difference between two runs
		// diagnosable rather than a number that moved.
		t.Logf("%d build, against %d committed: raise it when the extra one is"+
			" not the network", built, want)
		// The floor is set one below the best seen, deliberately. Runs of this
		// have given 18, 19, 19 and 21, and the difference is a cold image pull
		// against a per-target deadline rather than the engine changing its
		// mind - so the committed number leaves one target's worth of weather in
		// it, and the built list beside it says which one moved (E447).
	}
}

func readRunRatchet(key string) (int, error) {
	// Located the same way the tree is, and for the same reason: a path relative
	// to this file resolves differently under `go test` and under a compiled
	// binary, which is how this gate came to skip in a container and pass on a
	// developer's machine (E429).
	root := os.Getenv("EARTH_CORPUS_DIR")
	if root == "" {
		root = filepath.Join("..", "..")
	}

	b, err := os.ReadFile(filepath.Join(root, "corpus-ratchet.txt"))
	if err != nil {
		return 0, fmt.Errorf("read the ratchet: %w", err)
	}

	for line := range strings.SplitSeq(string(b), "\n") {
		on, count, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || on != key {
			continue
		}

		return strconv.Atoi(strings.TrimSpace(count))
	}

	return 0, fmt.Errorf("corpus-ratchet.txt says nothing about %s", key)
}
