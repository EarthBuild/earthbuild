package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// An IMPORT with a flag still names something.
//
// `IMPORT --allow-privileged github.com/org/repo:main` registers `repo` as a
// name this file may use. With the flag read as the reference, the name came
// from `--allow-privileged` - or from nothing - and the file's own
// `COPY repo+t/x .` then failed with *"repo was never imported"*, pointing at
// the line after the declaration that was right there (E440).
//
// The same shape as `ARG --global IMAGE=...` declaring an argument called
// `--global`, which this engine had and fixed: **a flag consumed as the
// positional argument, diagnosed at the use rather than at the declaration.**
func TestAnImportWithAFlagStillNamesSomething(t *testing.T) {
	t.Parallel()

	for _, imp := range []string{
		"IMPORT github.com/org/repo:main",
		"IMPORT --allow-privileged github.com/org/repo:main",
	} {
		f := remoteRepo(t, "")

		_, err := interp.Build(versioned+"\n"+imp+
			"\n\nmain:\n    FROM repo+build\n", testMain, interp.WithRemotes(f.fetch))
		if err != nil {
			t.Errorf("%s: %v", imp, err)
		}
	}
}

// A relative IMPORT names its last directory.
//
// `IMPORT ./a/really/deep/subdir` makes `subdir` the name, which is what the
// corpus writes and what a reader expects: the name is the directory, not the
// path to it.
func TestARelativeImportIsNamedByItsLastDirectory(t *testing.T) {
	t.Parallel()

	dir := ctxWith(t, map[string]string{
		"a/deep/subdir/Earthfile": versioned +
			"\nthere:\n    FROM alpine:3.22\n    RUN in-the-subdir\n",
	})

	p, err := interp.Build(versioned+
		"\nIMPORT ./a/deep/subdir\n\nmain:\n    FROM subdir+there\n",
		testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "in-the-subdir") {
		t.Errorf("the imported directory's target is not in the graph:\n%s", got)
	}
}
