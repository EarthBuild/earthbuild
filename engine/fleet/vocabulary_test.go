package fleet_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
)

// Every hint on the wire is one the specification names.
//
// **The vocabulary is closed** (green paper C.3), and a closed vocabulary that
// only one of the two documents knows about is not closed. Hints are how the
// driver tells a worker things it may act on - which peers hold the base, how
// big it is, what the step will read - and each is a field a second
// implementation has to be able to look up.
//
// It drifted the moment it mattered: C.3 listed masks, predicted reads and
// estimated duration, while the engine had grown `holders` (E260) and `bytes`
// (E317), both of them load-bearing for what a fleet does.
//
// Checked by reflection against the document, so adding a field to the struct
// fails until it is written down, and by reading the document's table, so
// naming a hint nothing sends fails too.
func TestEveryHintOnTheWireIsSpecified(t *testing.T) {
	t.Parallel()

	check(t, fleet.Hints{}, "### C.3 Assignments", "### C.3.1 Replies")
}

// Every field of a reply is one the specification names.
//
// The same argument as the hints, and the fields are the ones that matter most:
// `capacity`, `heldAt` and the three timing figures are the only measurements a
// driver has of a machine it does not own, and every placement decision is
// computed from them (E320, E317, E326). None of them was written down.
func TestEveryReplyFieldIsSpecified(t *testing.T) {
	t.Parallel()

	check(t, fleet.Reply{}, "### C.3.1 Replies", "### C.4 Transfer")
}

// check compares a wire type against the section of the specification that
// defines it, in both directions.
func check(t *testing.T, of any, from, to string) {
	t.Helper()

	spec := section(t, from, to)

	var named int

	for _, f := range reflect.VisibleFields(reflect.TypeOf(of)) {
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag == "" || tag == "-" {
			continue
		}

		named++

		if !strings.Contains(spec, "`"+tag+"`") {
			t.Errorf("the wire carries %q and the green paper's %s does not name"+
				" it\n  a closed vocabulary only one document knows is not"+
				" closed (E333, E334)", tag, from)
		}
	}

	if named == 0 {
		t.Fatal("no hints were found to check, so this test proves nothing")
	}

	// **And the other way.** A specification naming a hint nothing sends is a
	// promise to a peer that would wait for it - and the claim that this
	// direction was covered sat in this comment for one commit before the code
	// did, which is the failure class the whole project keeps meeting.
	for _, name := range namesIn(spec) {
		if !sends(of, name) {
			t.Errorf("the green paper's %s names %q and nothing sends it",
				from, name)
		}
	}
}

// namesIn is every wire field the specification's table names.
//
// A row may name several - "`layer`, `content`, `bytes`" is one line about one
// thing - so every backquoted word on a table line counts.
func namesIn(spec string) []string {
	var out []string

	for line := range strings.SplitSeq(spec, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "| `") {
			continue
		}

		for _, part := range strings.Split(strings.TrimSpace(line), "`")[1:] {
			if part != "" && !strings.ContainsAny(part, " ,|") {
				out = append(out, part)
			}
		}
	}

	return out
}

// sends reports whether the wire carries a hint of this name.
func sends(of any, name string) bool {
	for _, f := range reflect.VisibleFields(reflect.TypeOf(of)) {
		if tag, _, _ := strings.Cut(f.Tag.Get("json"), ","); tag == name {
			return true
		}
	}

	return false
}

// section is one part of the green paper, by its headings.
func section(t *testing.T, from, to string) string {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("..", "..", "docs-internals", "green-paper.md"))
	if err != nil {
		t.Fatalf("%v", err)
	}

	s := string(b)

	i, j := strings.Index(s, from), strings.Index(s, to)

	if i < 0 || j < 0 || j < i {
		t.Fatalf("the green paper has no %q before %q, so this test cannot"+
			" check anything", from, to)
	}

	return s[i:j]
}
