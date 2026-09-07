package exec

import (
	"strings"
	"testing"
)

// TestAPatternStagesUnderANameOfItsOwn.
//
// `SAVE ARTIFACT /output/* AS LOCAL .` reported success and wrote nothing: the
// destination is joined with the artifact's name the way a single file's is,
// giving `./output/*`, a path with a star in it.
//
// **The obvious fix is worse than the bug**, which is why this took two goes.
// Staging under the destination unchanged means `exports/<dest>`, and for
// `AS LOCAL .` that is the exports *root* - a directory holding every artifact
// this store has ever staged. Measured: a two-file build wrote thirteen
// unrelated files, from other tests, into the working directory.
//
// So a pattern gets a staging directory of its own, and the *contents* of that
// directory are copied out. A single artifact is untouched, because everything
// else about export depends on it.
func TestAPatternStagesUnderANameOfItsOwn(t *testing.T) {
	t.Parallel()

	plain := stagingFor("output", ".")
	if plain != "." {
		t.Errorf("a single artifact stages at %q, not the destination", plain)
	}

	one := stagingFor("/output/*", ".")
	two := stagingFor("/other/*", ".")

	for _, got := range []string{one, two} {
		if got == "." || got == "" || strings.ContainsAny(got, "*?[") {
			t.Errorf("a pattern stages at %q, which is either the exports root"+
				" or a path with a star in it", got)
		}
	}

	// Two patterns to one destination must not share a directory, or the second
	// export carries the first's files out with it.
	if one == two {
		t.Errorf("both patterns stage at %q", one)
	}

	// The same pattern twice is the same place, so a build does not litter the
	// store with one directory per invocation.
	if stagingFor("/output/*", ".") != one {
		t.Error("the staging directory is not a function of the request")
	}
}
