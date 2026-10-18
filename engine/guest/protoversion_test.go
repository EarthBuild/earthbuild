package guest

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// The protocol version is at least as high as the last bump it records.
//
// **The version is the only thing standing between a wire change and a silent
// disagreement.** The comment above `Version` says so at every step: version 3
// added mounts, and an older guest "would ignore an unknown field and run the
// step *without* its mount, which is a step that cannot see its cache reporting
// success"; version 16 added resource usage, and an older guest reports zero, so
// a build asked for `--exec-stats` says a step used no CPU at all.
//
// A test cannot know that a bump was *due* - that is a judgement about a change
// nobody has made yet. What it can do is hold the constant to the history the
// file already keeps: every bump is written down as "Version N added ...", so
// the constant must be at least the largest N. That catches the two ways this
// goes wrong in practice - a change made, the note written and the constant
// forgotten, and a merge that takes the constant backwards.
//
// Read out of the source rather than restated here, so the guard cannot drift
// from the thing it guards. The same trick the wire-vocabulary guard uses two
// files over.
func TestTheProtocolVersionMatchesItsOwnHistory(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("proto.go")
	if err != nil {
		t.Fatal(err)
	}

	bumps := regexp.MustCompile(`(?m)^// Version (\d+) added`).FindAllStringSubmatch(string(b), -1)
	if len(bumps) == 0 {
		t.Fatal("no bump is recorded in proto.go, so this guard reads nothing" +
			" - the comment's shape changed and took the check with it")
	}

	highest := 0
	for _, m := range bumps {
		n, convErr := strconv.Atoi(m[1])
		if convErr != nil {
			t.Fatalf("%q is not a version number: %v", m[1], convErr)
		}

		if n > highest {
			highest = n
		}
	}

	if Version < highest {
		t.Errorf("the protocol is version %d and its own notes describe a"+
			" version %d change"+
			"\n  a guest built before that change accepts these frames and"+
			" ignores what it does not know, which is the silent disagreement"+
			" the version check exists to turn into a refusal", Version, highest)
	}
}
