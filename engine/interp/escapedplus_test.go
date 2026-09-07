package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A backslash before a `+` means a file called `+`, not a target.
//
// `+` starts a target reference, so a path that contains one has to be able to
// say it does not: `COPY file-with-\+.txt ./` is the corpus's spelling, and
// `tests/escape.earth` exists for exactly this. This engine read the escape as
// part of the name, decided the source was a reference because it contained a
// `+`, and refused with *"file-with-+.txt" names a target but no artifact* -
// pointing at a file sitting in the build context (E441).
//
// The escape does not survive the lexer, so the order cannot be the fix: by the
// time the interpreter sees the argument, both spellings are one string. Shape
// decides instead - with no `/` after the `+` there is no artifact, so it cannot
// be the reference form - and the build context settles what is left.
func TestAnEscapedPlusIsPartOfTheFilename(t *testing.T) {
	t.Parallel()

	dir := ctxWith(t, map[string]string{"file-with-+.txt": "content"})

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    COPY file-with-\\+.txt ./\n",
		testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatalf("planning a copy of a file whose name contains a plus: %v", err)
	}

	// The escape is gone by the time anything copies it: the file on disk is
	// called `file-with-+.txt`, and a copy of `file-with-\+.txt` would find
	// nothing.
	got := describe(p.Graph.Nodes())
	if strings.Contains(got, `\+`) {
		t.Errorf("the plan still carries the backslash:\n%s", got)
	}
}

// An unescaped `+` still names a target.
//
// Asserted beside it, because a fix that made every `+` a filename would turn
// every artifact copy in every Earthfile into a missing file - and the corpus
// would say so one target later.
func TestAnUnescapedPlusStillNamesATarget(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+
		"\ndep:\n    FROM alpine:3.22\n    RUN make > /x\n    SAVE ARTIFACT /x x\n"+
		"\nmain:\n    FROM alpine:3.22\n    COPY +dep/x .\n", testMain)
	if err != nil {
		t.Fatalf("planning an artifact copy: %v", err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "make") {
		t.Errorf("the referenced target is not in the graph:\n%s", got)
	}
}

// And a `+` with no artifact after it still says so, when no such file exists.
//
// The diagnostic that was wrong here is the right one for the likelier mistake:
// `COPY +dep .` is a forgotten artifact path far more often than it is a file
// called `+dep`. Kept, and now says what the alternative would have been.
func TestAReferenceWithNoArtifactStillSaysSo(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\ndep:\n    FROM alpine:3.22\n\nmain:\n    FROM alpine:3.22\n    COPY +dep .\n",
		testMain)
	if err == nil {
		t.Fatal("COPY +dep planned, and there is no artifact in it")
	}

	if !strings.Contains(err.Error(), "names a target but no artifact") {
		t.Errorf("refused with %q, which is not the missing-artifact diagnostic", err)
	}
}

// The separator is the last `+`, because a target name cannot contain one.
//
// `COPY ./dir-with-\+-in-it+test/file.txt ./` is in the corpus, and the escape
// does not survive the lexer (E441) - so the engine sees two pluses and split at
// the first, making the path `./dir-with-` and the target `-in-it+test`, which
// named no Earthfile anywhere.
//
// The grammar decides it without needing the escape:
//
//	target-name  = 1*( ALPHA / DIGIT / "_" / "-" / "." )
//	target-ref   = [ target-path ] "+" target-name
//	artifact-ref = target-ref "/" artifact-path
//
// A target name has no `+` in it, so of the pluses before the artifact's `/`,
// **the last one is the separator**. That is a proof rather than a heuristic,
// which is what makes it safe to apply to every reference and not only to the
// ones that look odd (E444).
func TestTheSeparatorIsTheLastPlusBeforeTheArtifact(t *testing.T) {
	t.Parallel()

	dir := ctxWith(t, map[string]string{
		"dir-with-+-in-it/Earthfile": versioned +
			"\ntest:\n    FROM alpine:3.22\n    RUN make > /file.txt\n" +
			"    SAVE ARTIFACT /file.txt file.txt\n",
	})

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n"+
		"    COPY ./dir-with-+-in-it+test/file.txt ./\n",
		testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatalf("planning a copy from a target in a directory whose name has a"+
			" plus in it: %v", err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "make") {
		t.Errorf("the referenced target is not in the graph:\n%s", got)
	}
}

// A plus in the *artifact* path is not a separator.
//
// `+dep/a+b.txt` names the artifact `a+b.txt`, and only the pluses before the
// artifact's `/` are candidates - which is the half of the rule that would be
// lost by simply taking the last plus in the string.
func TestAPlusInTheArtifactPathIsNotASeparator(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+
		"\ndep:\n    FROM alpine:3.22\n    RUN make > /x\n    SAVE ARTIFACT /x a+b.txt\n"+
		"\nmain:\n    FROM alpine:3.22\n    COPY +dep/a+b.txt .\n", testMain)
	if err != nil {
		t.Fatalf("planning a copy of an artifact whose name has a plus: %v", err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "make") {
		t.Errorf("the referenced target is not in the graph:\n%s", got)
	}
}
