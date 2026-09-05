package interp_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ratchetAt is where the committed count lives.
//
// At the repository root rather than beside this test, because it is a fact
// about the project rather than about a package, and the test plan already
// names a sibling of it for the same purpose.
const ratchetAt = "corpus-ratchet.txt"

// ratchet fails when the corpus plans fewer targets than the committed count,
// and when it plans more.
//
// **489 targets plan today and nothing said so.** The corpus test's property is
// that every Earthfile is accepted or refused *actionably*, which is the right
// property and cannot tell a target that plans from one that refuses with a good
// message. A change that turned eighty into tidy refusals would pass it (E353).
//
// It fails on a **rise** as well, because a ratchet that lets an improvement
// pass unrecorded stops protecting the level that was reached, and the next
// regression is then measured against a number nobody has updated since. Moving
// it is one line, and whoever moves it has just earned the right to.
func ratchet(t *testing.T, planned, files int) {
	t.Helper()

	want, err := readRatchet(t)
	if err != nil {
		t.Errorf("%v\n  the count this project reached is not written down"+
			" anywhere, so nothing can notice it falling (E353)", err)

		return
	}

	switch {
	case planned < want:
		t.Errorf("%d of the corpus's targets plan, against %d committed in %s"+
			"\n  across %d Earthfiles. Something that used to build no longer"+
			" does, and the corpus sweep cannot see it because a refusal with a"+
			" good message is still a refusal (E353)",
			planned, want, ratchetAt, files)

	case planned > want:
		t.Errorf("%d targets plan and %s says %d - write the new number down",
			planned, ratchetAt, want)
	}
}

// readRatchet is the committed count for the platform this is running on.
//
// **Per platform, because the number is one.** The same corpus plans 489 targets
// on darwin and 481 on linux: some targets are conditional on the machine, so a
// single figure would either fail on one platform or be a floor so low it
// guarded nothing (E353).
//
// One line each, `<goos> <count>`, so the file reads as what it is - a
// measurement taken somewhere - rather than as a constant of the engine.
func readRatchet(t *testing.T) (int, error) {
	t.Helper()

	return readRatchetKey(t, runtime.GOOS)
}

// readRatchetKey reads one committed count by name.
//
// Keyed rather than positional so a slice of the corpus can have a ratchet of
// its own: `darwin` is every target, `darwin-docker` is the ones in Earthfiles
// using WITH DOCKER.
func readRatchetKey(t *testing.T, key string) (int, error) {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("..", "..", ratchetAt))
	if err != nil {
		return 0, err //nolint:wrapcheck // the path is in the message
	}

	for line := range strings.SplitSeq(string(b), "\n") {
		on, count, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || on != key {
			continue
		}

		return strconv.Atoi(strings.TrimSpace(count))
	}

	return 0, fmt.Errorf("%s says nothing about %s", ratchetAt, key)
}

// ratchetSlice is the ratchet for part of the corpus rather than all of it.
//
// **An aggregate can hide a whole construct's regression.** 192 of this
// repository's 489 planning targets are in Earthfiles using WITH DOCKER, from 27
// files. Break every one of them and the total falls by 39% - which would be
// noticed - but break the six that a subtler change touches and the total moves
// by one percent, which reads as noise (E389).
//
// Same rule as the whole-corpus ratchet, in both directions: a fall is a
// regression and a rise is a level worth recording, and moving the number is one
// line for whoever earned it.
// EnvRatchetList names a file to write the planned targets to when a slice's
// count does not match what is committed.
//
// **Why a list and not a better number.** The count moving says a target moved
// and not which one, and the two machines that disagree are usually not the
// same machine - a developer's and a runner's. Sorted, one per line, so
// `diff` finishes the diagnosis in a line.
const EnvRatchetList = "EARTH_RATCHET_LIST"

func ratchetSlice(t *testing.T, name string, planned int, plans ...string) {
	t.Helper()

	key := runtime.GOOS + "-" + name

	want, err := readRatchetKey(t, key)
	if err != nil {
		t.Errorf("%v\n  a slice of the corpus with no committed count is a slice"+
			" nothing is watching", err)

		return
	}

	switch {
	case planned < want:
		t.Errorf("%d %s targets plan, against %d committed in %s"+
			"\n  something that used to build no longer does, and the whole-corpus"+
			" count moves too little to show it", planned, name, want, ratchetAt)
	case planned > want:
		// **Not simply "move the number".** A rise is usually work, and moving
		// the number is then exactly right. But this count is not the same on
		// every machine - a target whose planning turns on installed tooling or
		// a feature probe plans on a developer's box and not on a runner - and
		// a number moved from a local run puts CI red on this same test in the
		// other direction. So say where the number comes from.
		t.Errorf("%d %s targets plan, against %d committed in %s"+
			"\n  more than before: move the number, which is the point of it -"+
			" but take it from CI"+
			"\n  a rise seen only on this machine is a target that plans here"+
			" and not on a runner, and committing it turns CI red the other way"+
			"\n  set %s to a path to write the planned targets, and diff the"+
			" two machines' lists to see which target moved",
			planned, name, want, ratchetAt, EnvRatchetList)
	}

	// Written on any mismatch, in either direction, because the question is the
	// same one both ways: which target moved.
	if at := os.Getenv(EnvRatchetList); at != "" && planned != want && len(plans) > 0 {
		sorted := append([]string(nil), plans...)
		sort.Strings(sorted)

		err := os.WriteFile(at, []byte(strings.Join(sorted, "\n")+"\n"), 0o600)
		if err != nil {
			t.Errorf("write the planned targets to %s: %v", at, err)

			return
		}

		t.Logf("the %d planned targets are in %s, sorted; diff it against the"+
			" same file from the machine that disagrees", len(sorted), at)
	}
}
