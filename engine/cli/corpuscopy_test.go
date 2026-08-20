package cli_test

import (
	"os"
	"path/filepath"
	"testing"
)

// The corpus is built in a copy, so a sweep leaves the repository as it found
// it.
//
// `SAVE ARTIFACT ... AS LOCAL` writes where the Earthfile says, which for a
// corpus of tutorials is next to their sources. A full sweep of 130 targets
// produced 35 files - jars, bundled javascript, compiled binaries, a
// `package.json` an Earthfile writes back - all of them untracked, all of them
// looking exactly like work when staged. 58,000 lines of build output came
// within one `git add -A` of a commit.
//
// A test that leaves artefacts behind is a test that has to be remembered
// about, which is the same as one that is forgotten.
func TestTheCorpusIsBuiltInACopy(t *testing.T) {
	t.Parallel()

	root := corpusRoot(t)

	// Somewhere other than the tree the test was pointed at.
	real, err := filepath.Abs(filepath.Join("..", "..", "examples"))
	if err != nil {
		t.Fatal(err)
	}

	got, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}

	if got == real {
		t.Fatal("the corpus is built where it lives, so a sweep writes into the repository")
	}

	// A copy that is missing the corpus is a copy that quietly measures
	// nothing: the sweep would report zero targets and look like a pass.
	_, err = os.Stat(filepath.Join(root, "js", testEarthfile))
	if err != nil {
		t.Errorf("the copy does not hold the corpus: %v", err)
	}

	// And it holds what the corpus *refers to*. Earthfiles in a monorepo reach
	// upwards - `FROM ../..+base` is ordinary - so a copy of the subtree alone
	// breaks every one of them with "no Earthfile for this reference". Copying
	// from the repository root is what makes those resolve, and this is the
	// assertion that says so: the file two levels up must be there.
	_, err = os.Stat(filepath.Join(root, "..", testEarthfile))
	if err != nil {
		t.Errorf("the copy does not hold what the corpus refers to: %v", err)
	}
}

// The copy leaves out what a build would only make again.
//
// `examples/` alone is 958 MB on this machine, nearly all of it node_modules
// and .next left by builds. Copying that per sweep costs more than the sweep,
// and none of it is input: a build that needs node_modules installs them.
func TestTheCorpusCopyLeavesOutBuildOutput(t *testing.T) {
	t.Parallel()

	root := corpusRoot(t)

	for _, junk := range []string{"node_modules", ".next"} {
		found := 0

		_ = filepath.Walk(filepath.Join(root, ".."), func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil //nolint:nilerr // an unreadable corner is not the point here
			}

			if fi.IsDir() && fi.Name() == junk {
				found++

				return filepath.SkipDir
			}

			return nil
		})

		if found > 0 {
			t.Errorf("the copy holds %d %s directories, which no build reads", found, junk)
		}
	}
}
