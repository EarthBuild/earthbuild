package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

// List prints the names of every target an Earthfile offers, one per line.
//
// Not a build: nothing is planned, nothing is executed, and no sandbox is
// started. It is the question "what can I ask for here", and it is answered from
// the file alone (E474).
//
// Sorted and `+`-prefixed, and the base recipe is named among them. All three
// are fixed by `tests/Earthfile`, which diffs the whole output against a list:
// `tests/ls.earth` declares alpha, charlie, bravo and no `base` at all, so the
// order is not the file's and the base recipe is a target as far as this is
// concerned - which is also true of `+base` as a reference anywhere else.
func List(o Options) error {
	tree, err := readTree(o.Dir)
	if err != nil {
		return err
	}

	names := make([]string, 0, len(tree.Targets)+1)
	names = append(names, earthfile.TargetBase)

	for _, t := range tree.Targets {
		// A file may declare `base:` itself, and then it is one target rather
		// than two lines saying the same thing.
		if t.Name != earthfile.TargetBase {
			names = append(names, t.Name)
		}
	}

	sort.Strings(names)

	// Nowhere by default, as a build's progress is: a caller that wants the
	// answer says where to put it. `Run` has the same rule, and a listing that
	// went to stdout regardless would be the one command here that decides for
	// its caller.
	out := io.Writer(io.Discard)
	if o.Out != nil {
		out = o.Out
	}

	for _, name := range names {
		_, err := fmt.Fprintf(out, "+%s\n", name)
		if err != nil {
			return err
		}
	}

	return nil
}

// readTree parses the Earthfile of a directory.
//
// Shared by the commands that read a file and do not build it. The path is in
// the error because "no Earthfile" is a question about *where*: the answer is
// almost always that the directory is not the one the author meant.
func readTree(dir string) (earthfile.Tree, error) {
	if dir == "" {
		dir = "."
	}

	path := filepath.Join(dir, "Earthfile")

	src, err := os.ReadFile(path) //nolint:gosec // the directory the caller named
	if err != nil {
		return earthfile.Tree{}, fmt.Errorf("no Earthfile to read\n  looked for %s", path)
	}

	tree, err := earthfile.Parse(path, string(src), earthfile.WithSourceMap())
	if err != nil {
		return earthfile.Tree{}, fmt.Errorf("parse %s: %w", path, err)
	}

	return tree, nil
}
