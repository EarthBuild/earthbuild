package cli_test

import (
	"errors"
	"io/fs"
	"os"
	"testing"
)

// A walk of the source tree survives a file that is being generated in it.
//
// Three tests failed at once running this repository's own suite under the
// native engine on a 32-core linux box, all with the same shape:
//
//	corpuscopy_test.go:24: copy the corpus: lstat
//	  /earthly/engine/store/testdata/bigtree-20000.building-26675/d35/e50/f11427:
//	  no such file or directory
//
// `engine/store` generates a hundred-thousand-file fixture into gitignored
// `testdata/` and renames it into place, deliberately: it is a `for` loop, not
// something to commit, and caching it there is what makes the second run cheap.
// So while `engine/store` builds it, `engine/cli` walks past it - and
// `filepath.Walk` hands an `lstat` failure to the callback, which returned it
// and failed the test (E616).
//
// It is a race, so it needs a machine with enough cores to lose: darwin's suite
// has never shown it and CI's ran `|| true` and never said.
func TestAVanishedFileIsNotACorpusFailure(t *testing.T) {
	t.Parallel()

	_, err := os.Lstat("this-path-does-not-exist")
	if err == nil {
		t.Fatal("a path that does not exist was stat-able")
	}

	if !vanished(err) {
		t.Errorf("a missing file was not recognised as vanished: %v", err)
	}

	if vanished(nil) {
		t.Error("no error at all was read as a vanished file")
	}

	// Everything else still fails the walk. A permission error is a corpus this
	// engine cannot read, and skipping it silently would copy a tree that is
	// missing a directory nobody was told about.
	if vanished(errors.New("something else")) || vanished(fs.ErrPermission) {
		t.Error("an error that is not a missing file was skipped")
	}
}

// A generated fixture is not corpus input.
//
// `skipInCorpus` already names what a build makes rather than reads -
// `node_modules`, `.next`, `vendor`. The fixture belongs there on the same
// argument, and it is the reason the walk meets a vanishing file at all: not
// copying it is both the fix and a saving of eighty megabytes per sweep.
//
// Matched by prefix because the staging name carries a pid -
// `bigtree-20000.building-26675` - and it is precisely the staging directory,
// the transient one, that a walk trips over.
func TestAGeneratedFixtureIsNotCorpusInput(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"bigtree-500", "bigtree-20000.building-26675", "node_modules", ".git"} {
		if !skipCorpusDir(name) {
			t.Errorf("%s is copied into the corpus, and no build reads it", name)
		}
	}

	for _, name := range []string{"engine", "testdata", "bigtreeish", "docs"} {
		if skipCorpusDir(name) {
			t.Errorf("%s was left out of the corpus, and a build may read it", name)
		}
	}
}
