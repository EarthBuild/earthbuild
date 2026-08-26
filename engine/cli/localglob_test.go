package cli

import (
	"testing"
)

// TestTheNaiveFixForAPatternDestinationIsRefused.
//
// `SAVE ARTIFACT /output/* AS LOCAL .` reports success and writes nothing: the
// destination is joined with the artifact's name the way a single file's is,
// giving `./output/*` - a path with a star in it, which nothing is called.
// **[GAP]**, and this test is not the fix.
//
// **It guards against the obvious fix, which is worse than the bug.** Returning
// the destination unchanged makes `exportTo` stage into `exports/<dest>`, and
// for `AS LOCAL .` that is the exports *root* - a directory holding every
// artifact this store has ever staged. The build then copies all of it into the
// project: measured, a two-file build wrote thirteen unrelated files from other
// tests into the working directory. Writing the wrong files is worse than
// writing none.
//
// A test rather than a comment, for the reason E34 gives: a paragraph cannot
// notice when somebody stops obeying it. A pattern has to stage under a name of
// its own, in `exportTo`, with the *contents* of that directory copied out.
func TestTheNaiveFixForAPatternDestinationIsRefused(t *testing.T) {
	t.Parallel()

	// The bug, asserted as it stands: it writes nothing, which is safe.
	if got := localPath(".", "/output/*"); got == "." {
		t.Error("a pattern destination is the bare directory, so the export" +
			" stages into the exports root and copies every artifact this" +
			" store has ever held into the project")
	}

	// Unchanged for a single artifact, which is what every other SAVE ARTIFACT
	// relies on.
	for _, c := range []struct{ dest, artifact, want string }{
		{".", "output", "output"},
		{"./there.txt", "output", "./there.txt"},
		{".", "", "."},
	} {
		if got := localPath(c.dest, c.artifact); got != c.want {
			t.Errorf("localPath(%q, %q) = %q, want %q",
				c.dest, c.artifact, got, c.want)
		}
	}
}
