package corpus_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/internal/corpus"
)

// The tree's own account of how its files are meant to be built.
//
// Read by two things now: the run gate, which drives every invocation, and the
// planning sweep, which needs to know which targets are *meant* to be refused so
// that the engine refusing them stops reading as work left to do (E477).
//
// One reader rather than two, because two would be two opinions about what the
// tree says - and the sweep's would be the one nobody was maintaining.
func TestTheTreesInvocationsAreRead(t *testing.T) {
	t.Parallel()

	got := corpus.Invocations(treeSource(t))

	if len(got) < 250 {
		t.Fatalf("only %d invocations found; the tree declares hundreds, so"+
			" the reader is broken rather than the tree", len(got))
	}

	// A line-continued invocation, which a third of the tree is written as.
	// Read line by line these are a `DO` with no flags followed by fragments
	// mentioning no command (E454).
	var continued, failing, withFile int

	for _, in := range got {
		if in.File != "" {
			withFile++
		}

		if in.ShouldFail {
			failing++
		}

		if len(in.Extra) > 0 {
			continued++
		}
	}

	if failing < 70 {
		t.Errorf("only %d invocations say --should_fail; the tree declares"+
			" seventy-odd, and each one this reader misses is a refusal counted"+
			" as a defect", failing)
	}

	if withFile < 200 {
		t.Errorf("only %d invocations name a file, and a file named once is"+
			" used until the next one (E470)", withFile)
	}

	if continued < 50 {
		t.Errorf("only %d invocations carry extra arguments; the reader is"+
			" probably stopping at the first space", continued)
	}
}

// A file named once is used until the next one, and a target header resets it.
func TestAFileNamedOnceIsUsedUntilTheNext(t *testing.T) {
	t.Parallel()

	got := corpus.Invocations(`
first:
    DO +RUN_EARTH --earthfile=a.earth --target=+one
    DO +RUN_EARTH --target=+two

second:
    DO +RUN_EARTH --target=+three
`)

	want := []struct{ file, target string }{
		{"a.earth", "one"},
		{"a.earth", "two"},
		{"", "three"},
	}

	if len(got) != len(want) {
		t.Fatalf("read %d invocations, want %d: %+v", len(got), len(want), got)
	}

	for i, w := range want {
		if got[i].File != w.file || got[i].Target != w.target {
			t.Errorf("invocation %d is %s+%s, want %s+%s",
				i, got[i].File, got[i].Target, w.file, w.target)
		}
	}
}

// treeSource reads `tests/Earthfile`.
func treeSource(t *testing.T) string {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("..", "..", "tests", "Earthfile"))
	if err != nil {
		t.Skipf("no corpus here: %v", err)
	}

	return string(b)
}

// The tree names which of its targets are meant to be refused.
//
// The planning sweep needs this: `tests/save-artifact-dont-overwrite.earth` has
// six targets whose whole purpose is to be refused, and the sweep had been
// counting the engine refusing them as work left to do. **A refusal counted as a
// gap is a number that cannot reach zero** (E477).
func TestTheTargetsMeantToFailAreNamed(t *testing.T) {
	t.Parallel()

	meant := corpus.MeantToFail(treeSource(t))

	for _, want := range []string{
		"save-artifact-dont-overwrite.earth+dont-overwrite-root",
		"save-artifact-dont-overwrite.earth+dont-overwrite-rel-ref",
		"builtin-args-invalid-default.earth+test",
	} {
		if !meant[want] {
			t.Errorf("%s is driven with --should_fail and is not in the set", want)
		}
	}

	// And a target the tree expects to build is not in it, or the set says
	// everything and means nothing.
	if meant["copy.earth+copy-wildcard"] {
		t.Error("copy.earth+copy-wildcard is expected to build, and the set" +
			" claims it must fail")
	}

	if len(meant) < 40 {
		t.Errorf("only %d targets are named; seventy-odd invocations say"+
			" --should_fail, and they name fewer targets than that but not this"+
			" few", len(meant))
	}
}
