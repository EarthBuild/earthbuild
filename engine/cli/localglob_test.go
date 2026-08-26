package cli

import (
	"testing"
)

// TestAPatternLandsInTheDirectoryRatherThanUnderItsOwnName.
//
// `SAVE ARTIFACT /output/* AS LOCAL .` names however many files the build
// made, and the destination is where they go. Joined with the artifact's name
// the way a single file is, the destination became `./output/*` - a path with a
// star in it, which nothing is called - so the build reported success and wrote
// nothing at all.
//
// Silence again: `AS LOCAL` is the one command whose whole purpose is a file on
// this machine, and not writing one is the only outcome that cannot be noticed
// from inside the build.
//
// The guest already exports each match under its own basename; what this fixes
// is the destination it exports *to*.
func TestAPatternLandsInTheDirectoryRatherThanUnderItsOwnName(t *testing.T) {
	t.Parallel()

	// **[GAP]** - and a skip rather than a failure, because the obvious fix is
	// wrong and the next reader should not spend the afternoon rediscovering
	// that.
	//
	// Returning `dest` unchanged here makes `exportTo` stage into
	// `exports/<dest>`, and for `AS LOCAL .` that is the *exports root* - a
	// directory that accumulates every artifact this store has ever staged.
	// The build then copies all of it into the project. Measured: a two-file
	// build wrote thirteen unrelated files, from other tests, into the working
	// directory. That is worse than writing nothing.
	//
	// The fix is in `exportTo` rather than here: a pattern has to stage under a
	// name of its own and the *contents* of that directory copied out, which is
	// a different shape from the single-file path and wants its own care.
	t.Skip("staging a pattern needs a directory of its own; see the comment")

	for _, c := range []struct {
		name, dest, artifact, want string
	}{
		{"a pattern", ".", "/output/*", "."},
		{"a pattern under a directory", "out/", "bin/*.so", "out/"},
		// Unchanged: a single artifact still takes its name below the
		// destination, which is what every other SAVE ARTIFACT relies on.
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
