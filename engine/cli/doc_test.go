package cli_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// `doc` prints the documented targets and their documentation.
//
// `tests/Earthfile` pins the shape with a multiline pcregrep, which fixes the
// header, both indents and - because the pattern contains a bare newline pair -
// that a blank line inside the documentation stays blank rather than becoming
// six spaces (E474):
//
//	TARGETS:
//	  +documented-target
//	      documented-target is a target with documentation
//	      ...
//
// Undocumented targets are absent, and so is one whose comment does not begin
// with its own name: `tests/target-docs.earth` has both, and the parser already
// applies that rule, which is why this is formatting rather than judgement.
func TestDocPrintsDocumentedTargets(t *testing.T) {
	t.Parallel()

	out := docOf(t, "../../tests/target-docs.earth", cli.Options{})

	const want = "TARGETS:\n" +
		"  +documented-target\n" +
		"      documented-target is a target with documentation\n" +
		"      that spans multiple lines.\n" +
		"\n" +
		"      It also has a separator between paragraphs.\n"

	if !strings.Contains(out, want) {
		t.Errorf("doc printed\n%s\nand the tree greps for\n%s", out, want)
	}

	for _, absent := range []string{"undocumented-target", "incorrectly-documented-target"} {
		if strings.Contains(out, absent) {
			t.Errorf("doc named %s, which has no documentation of its own", absent)
		}
	}
}

// A blank line in the documentation is blank, not indented whitespace.
//
// Asserted apart from the shape above because it is the part a formatter breaks
// silently: six spaces on an empty line look identical in a terminal and stop
// the tree's `\n\n` matching, so the corpus target fails with a diff nobody can
// see.
func TestDocLeavesABlankLineBlank(t *testing.T) {
	t.Parallel()

	out := docOf(t, "../../tests/target-docs.earth", cli.Options{})

	for line := range strings.SplitSeq(out, "\n") {
		if line != strings.TrimRight(line, " \t") {
			t.Errorf("a line ends in whitespace: %q", line)
		}
	}
}

// `doc --long` adds what a caller has to supply and what the target produces.
//
// The tree greps for two of its section headers. Only two, which is the bound of
// what is witnessed: the rest of the shape is this engine's, and the corpus
// says nothing about it.
func TestDocLongNamesArgumentsAndImages(t *testing.T) {
	t.Parallel()

	out := docOf(t, "../../tests/doc-recipe-block.earth", cli.Options{Long: true})

	for _, want := range []string{"REQUIRED ARGS:", "IMAGES:"} {
		if !strings.Contains(out, want) {
			t.Errorf("doc --long printed no %q section:\n%s", want, out)
		}
	}
}

// The short form has neither, because that is the difference between them.
func TestDocShortOmitsTheRecipeBlock(t *testing.T) {
	t.Parallel()

	out := docOf(t, "../../tests/doc-recipe-block.earth", cli.Options{})

	for _, absent := range []string{"REQUIRED ARGS:", "IMAGES:", "ARTIFACTS:"} {
		if strings.Contains(out, absent) {
			t.Errorf("doc printed a %q section without --long, so the flag"+
				" decides nothing:\n%s", absent, out)
		}
	}
}

// docOf runs `doc` over a corpus file and returns what it printed.
func docOf(t *testing.T, path string, o cli.Options) string {
	t.Helper()

	dir := t.TempDir()

	src, err := os.ReadFile(path)
	if err != nil {
		// **The fixture is not in every copy of this repository.** `+unit-test`
		// builds against a tree the Earthfile assembled, and it does not copy
		// `tests/` - so this reads a path that is simply not there, and failing
		// says the documentation is wrong when nobody looked at it (E604, E605).
		if errors.Is(err, fs.ErrNotExist) {
			t.Skipf("%s is not in this copy of the repository", path)
		}

		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(dir, "Earthfile"), src, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	o.Dir, o.Out = dir, &out

	err = cli.Doc(o)
	if err != nil {
		t.Fatalf("documenting %s: %v", path, err)
	}

	return out.String()
}

// A comment documents what it names, and nothing else.
//
// `tests/doc-recipe-block.earth` says so in its own words three times: `# this
// is an undocumented argument.`, `# this is an undocumented artifact.` and
// `# this is an undocumented image.` are all comments sitting directly above the
// thing they are called undocumented for. The rule that makes them so is the one
// the parser already applies to targets - the text must begin with the name -
// and it applies to what a recipe declares as well (E474).
//
// Without it, every comment in the file reads as documentation, and a reader
// asking what an argument is for gets a sentence about something else.
func TestDocIgnoresACommentThatNamesSomethingElse(t *testing.T) {
	t.Parallel()

	out := docOf(t, "../../tests/doc-recipe-block.earth", cli.Options{Long: true})

	for _, absent := range []string{
		"this is an undocumented argument",
		"this is an undocumented artifact",
		"this is an undocumented image",
	} {
		if strings.Contains(out, absent) {
			t.Errorf("doc printed %q, and the file calls that thing"+
				" undocumented\n%s", absent, out)
		}
	}

	// The ones that do name themselves are still there, so the rule is a filter
	// rather than a switch that turned everything off.
	for _, want := range []string{
		"withDocs - withDocs is a documented argument",
		"bar.txt - bar.txt is a documented artifact",
		"baz - baz is a documented image",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doc printed no %q, and that comment names its own"+
				" subject\n%s", want, out)
		}
	}

	// A SAVE IMAGE naming two tags is documented by a comment that names either
	// of them: the file's own comment says as much, and calls the second one
	// out by name.
	if !strings.Contains(out, "eggs - eggs is just one of the image names") {
		t.Errorf("doc dropped the documentation of a multiple-name SAVE"+
			" IMAGE\n%s", out)
	}
}
