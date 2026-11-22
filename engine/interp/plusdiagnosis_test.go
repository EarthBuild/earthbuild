package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A source that cannot be a target reference is diagnosed as a file.
//
// `COPY file-with-\+.txt ./` is how an Earthfile writes a filename containing a
// `+`. The escape does not survive the lexer, so by the time the interpreter
// sees it the two spellings are one string - and the shape settles it: with no
// `/` after the `+` there is no artifact, so it cannot be the reference form
// whatever the author meant (E441).
//
// Having decided that, the engine said the opposite when the file was missing:
//
//	"file-with-+.txt" names a target but no artifact
//	  write it as +target/path
//
// which sends the reader looking for a target the engine has already worked out
// cannot exist. The same shape as E478's Dockerfile: **a diagnosis about the
// thing that was ruled out**. The missing file is the finding; the reference
// reading is the aside (E479).
func TestAPlusWithNoArtifactIsDiagnosedAsAMissingFile(t *testing.T) {
	t.Parallel()

	dir := ctxWith(t, map[string]string{"other.txt": "x"})

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    COPY file-with-+.txt ./\n",
		testMain, interp.WithContext(dir))
	if err == nil {
		t.Fatal("a copy of a file the context does not have was planned")
	}

	got := err.Error()

	for _, want := range []string{
		"file-with-+.txt",
		// What failed, and where it looked.
		"build context",
		dir,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("refused with %q, which does not say %q", got, want)
		}
	}

	// The claim is about the file. A reader who is told first that they named a
	// target goes looking for one.
	if strings.HasPrefix(strings.SplitN(got, "\n", 2)[0], `"file-with-+.txt" names a target`) {
		t.Errorf("the first line claims a target: %q", strings.SplitN(got, "\n", 2)[0])
	}

	// And the other reading is still offered, because a `+` in a source is
	// worth a second thought even when the shape rules it out.
	if !strings.Contains(got, "+") || !strings.Contains(got, "target") {
		t.Errorf("refused with %q, and never mentions the other reading", got)
	}
}

// A source that *could* be a reference is still diagnosed as one.
//
// `+dep/x` has an artifact path after the `+`, so the shape says reference and
// the reader is looking for a target. Asserted beside the other, because a fix
// that made every missing source a missing file would bury the commonest
// mistake in an Earthfile: a target name that is not there.
func TestAReferenceShapedSourceIsStillDiagnosedAsAReference(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    COPY +nosuch/x .\n", testMain)
	if err == nil {
		t.Fatal("a copy from a target that does not exist was planned")
	}

	if !strings.Contains(err.Error(), "nosuch") {
		t.Errorf("refused with %q, which does not name the target", err)
	}
}

// Which of the two diagnoses is right depends on what is *before* the plus.
//
// `COPY +dep .` is a forgotten artifact path far more often than it is a file
// called `+dep`, and `COPY ../+base .` is the same thing with a directory in
// front - both are reference-shaped, and both should send the reader after the
// artifact. `file-with-+.txt` is not: what precedes the plus is a filename
// rather than a path or an alias.
//
// So the rule is what sits to the left: nothing, a path, or an IMPORT alias
// means a reference; anything else means a name that happens to contain a plus
// (E479).
func TestWhatPrecedesThePlusDecidesTheDiagnosis(t *testing.T) {
	t.Parallel()

	dir := ctxWith(t, map[string]string{"other.txt": "x"})

	for name, tc := range map[string]struct {
		src      string
		artifact bool
	}{
		"a bare plus":       {src: "+dep", artifact: true},
		"a relative path":   {src: "../+base", artifact: true},
		"a filename":        {src: "file-with-+.txt"},
		"a filename with -": {src: "another-file-with-+.txt"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+
				"\nmain:\n    FROM alpine:3.22\n    COPY "+tc.src+" ./\n",
				testMain, interp.WithContext(dir))
			if err == nil {
				t.Fatalf("%q was planned, and there is nothing to plan", tc.src)
			}

			artifact := strings.Contains(err.Error(), "names a target but no artifact")
			if artifact != tc.artifact {
				t.Errorf("%q is diagnosed as %s:\n%v", tc.src,
					map[bool]string{true: "a missing artifact", false: "a missing file"}[artifact],
					err)
			}
		})
	}
}
