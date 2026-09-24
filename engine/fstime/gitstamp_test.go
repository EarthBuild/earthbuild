package fstime

import (
	"strings"
	"testing"
	"time"
)

// The stream `git log --format=$'\x01%ct' --name-only --no-renames -z` writes:
// NUL-separated tokens, a commit time marked by the leading \x01, and the first
// path of each commit carrying the newline the format left behind.
//
// Parsed from a recorded sample rather than a live repository, because the shape
// is the thing that can change under us - a git that stops emitting that newline
// would otherwise be found by a build, not by a test.
func TestHistoryGivesEachPathTheCommitThatLastChangedIt(t *testing.T) {
	t.Parallel()

	stream := strings.Join([]string{
		"\x011700000300\x00", "\nsrc/edited.rs\x00", "src/also.rs\x00",
		"\x011700000200\x00", "\nsrc/older.rs\x00", "src/edited.rs\x00",
		"\x011700000100\x00", "\nsrc/oldest.rs\x00",
	}, "")

	want := map[string]bool{
		"src/edited.rs": true,
		"src/also.rs":   true,
		"src/older.rs":  true,
		"src/oldest.rs": true,
	}

	got := commitTimesIn(strings.NewReader(stream), want)

	for path, sec := range map[string]int64{
		// Newest first, so the first sighting wins: `edited.rs` appears in two
		// commits and takes the later one.
		"src/edited.rs": 1_700_000_300,
		"src/also.rs":   1_700_000_300,
		"src/older.rs":  1_700_000_200,
		"src/oldest.rs": 1_700_000_100,
	} {
		if !got[path].Equal(time.Unix(sec, 0)) {
			t.Errorf("%s got %v, wanted %v", path, got[path], time.Unix(sec, 0))
		}
	}
}

// A path nobody asked about is not carried, and the walk stops as soon as every
// wanted path has a time: a full history is O(commits) and the tail of it says
// nothing about the tree being packed.
func TestHistoryStopsOnceEveryWantedPathHasATime(t *testing.T) {
	t.Parallel()

	rest := strings.Repeat("\x011600000000\x00\nsrc/noise.rs\x00", 10_000)
	stream := "\x011700000300\x00\nsrc/only.rs\x00" + rest

	r := strings.NewReader(stream)
	got := commitTimesIn(r, map[string]bool{"src/only.rs": true})

	if len(got) != 1 {
		t.Fatalf("carried %d paths, wanted only the one asked for: %v", len(got), got)
	}

	if r.Len() == 0 {
		t.Error("read the whole stream; wanted it abandoned once the wanted path was found")
	}
}

// `git status --porcelain -z` writes a two-letter state, a space, and the path,
// NUL-separated - and names both the modified and the untracked in one answer.
func TestTheDirtyListIsReadFromTheStatusCodes(t *testing.T) {
	t.Parallel()

	got := dirtyIn(strings.NewReader(" M a/one.rs\x00?? b/two.rs\x00A  c/three.rs\x00"))

	for _, at := range []string{"a/one.rs", "b/two.rs", "c/three.rs"} {
		if !got[at] {
			t.Errorf("%s was not read as dirty, from %v", at, got)
		}
	}

	if len(got) != 3 {
		t.Errorf("read %v", got)
	}

	// An empty answer is ordinary: git writes nothing at all when a tree is
	// clean, not an empty record.
	if len(dirtyIn(strings.NewReader(""))) != 0 {
		t.Error("a clean tree read as dirty")
	}

	// Too short to carry a path. Nothing downstream can tell a truncated record
	// from a file called "M", so it is dropped rather than guessed at.
	if len(dirtyIn(strings.NewReader(" M\x00"))) != 0 {
		t.Error("a truncated record was read as a path")
	}
}

// The three-way choice, which is the whole of the design: history for what is
// committed, the local clock for what is not, and the fixed epoch for anything
// with no honest answer.
func TestWhichClockEachPathIsGiven(t *testing.T) {
	t.Parallel()

	committed := time.Unix(1_700_000_000, 0)
	onDisk := time.Unix(1_800_000_000, 123)

	at := chooseStamp(
		map[string]time.Time{"src/clean.rs": committed},
		map[string]bool{"src/dirty.rs": true},
		func(string) (time.Time, bool) { return onDisk, true },
	)

	for _, tc := range []struct {
		name, path string
		want       time.Time
	}{
		// Committed and untouched: the same answer on every machine that has
		// this commit, which is what makes a context digest shareable.
		{name: "clean", path: "src/clean.rs", want: committed},

		// Locally modified: no shared answer exists, and asking for one would
		// be inventing it. A dirty tree is not reproducible by definition, so
		// the local clock costs nothing and is what the compiler needs.
		{name: "dirty", path: "src/dirty.rs", want: onDisk},

		// Neither: a file git has been told to ignore. Its content is already
		// only on this machine, so the disk is as good an answer as exists.
		{name: "ignored", path: "target/out", want: onDisk},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := at(tc.path); !got.Equal(tc.want) {
				t.Errorf("%s got %v, wanted %v", tc.path, got, tc.want)
			}
		})
	}

	// Unstattable - a path that has gone between the walk and the pack. The
	// epoch is the one answer that cannot be wrong about ordering, because it
	// precedes everything.
	gone := chooseStamp(nil, nil, func(string) (time.Time, bool) { return time.Time{}, false })
	if got := gone("vanished"); !got.Equal(time.Unix(1, 0).UTC()) {
		t.Errorf("got %v, wanted the epoch", got)
	}
}
