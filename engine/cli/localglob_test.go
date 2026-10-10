package cli

import (
	"testing"
)

// TestAPatternLandsInTheDirectoryAndStagesApart.
//
// `SAVE ARTIFACT /output/* AS LOCAL .` names however many files the build made,
// so the destination is where they go rather than what they are called. Joined
// the way a single file is, it wrote to `./output/*` - a path with a star in
// it - and the build reported success having written nothing.
//
// **This half was refused until the other half existed**, and the refusal was
// right. Returning the destination alone made `exportTo` stage into
// `exports/<dest>`, and for `AS LOCAL .` that is the exports *root*: measured,
// a two-file build wrote thirteen unrelated files from other tests into the
// working directory. Writing the wrong files is worse than writing none.
//
// `exec.stagingFor` now gives a pattern a directory of its own and the contents
// are copied out, so this is the correct half of a pair rather than the
// dangerous half of one.
func TestAPatternLandsInTheDirectoryAndStagesApart(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name, dest, artifact, want string
	}{
		{"a pattern", ".", "/output/*", "."},
		{"a pattern under a directory", "out/", "bin/*.so", "out/"},
		// Unchanged for a single artifact, which everything else relies on.
		{"one file into a directory", ".", "output", "output"},
		{"one file renamed", "./there.txt", "output", "./there.txt"},
		{"no name", ".", "", "."},
	} {
		if got := localPath(c.dest, c.artifact); got != c.want {
			t.Errorf("%s: localPath(%q, %q) = %q, want %q",
				c.name, c.dest, c.artifact, got, c.want)
		}
	}
}
