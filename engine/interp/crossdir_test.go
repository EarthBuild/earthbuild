package interp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// monorepo writes two Earthfiles, one referring to the other's target.
func monorepo(t *testing.T) string {
	t.Helper()

	root := t.TempDir()

	err := os.MkdirAll(filepath.Join(root, "html"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	// The file the referenced target copies lives beside *its* Earthfile.
	err = os.WriteFile(filepath.Join(root, "html", "index.html"), []byte("<h1>hi</h1>"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(root, "html", testEarthfile), []byte(`VERSION 0.8
html:
    FROM alpine:3.22
    COPY index.html ./
    SAVE ARTIFACT index.html
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return root
}

// A target in another directory reads its own directory, not the caller's.
//
// `./html+html` copies `index.html`, and that name means the file beside
// html/Earthfile. Resolving it against the *referring* Earthfile's directory
// looked for it one level up, where it is not - and the error named a path
// nobody had written, in a file the author had not been reading.
//
// This is what a monorepo is: an Earthfile per component, each referring to its
// neighbours.
func TestAReferencedTargetReadsItsOwnDirectory(t *testing.T) {
	t.Parallel()

	root := monorepo(t)

	src := `VERSION 0.8
site:
    FROM alpine:3.22
    COPY ./html+html/index.html ./
`

	err := os.WriteFile(filepath.Join(root, testEarthfile), []byte(src), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	_, err = interp.Build(src, "site", interp.WithContext(root))
	if err != nil {
		t.Fatalf("a target in another directory could not read its own files: %v", err)
	}
}

// And a BUILD of it does the same.
func TestABuiltTargetReadsItsOwnDirectory(t *testing.T) {
	t.Parallel()

	root := monorepo(t)

	src := `VERSION 0.8
all:
    BUILD ./html+html
`

	err := os.WriteFile(filepath.Join(root, testEarthfile), []byte(src), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	_, err = interp.Build(src, "all", interp.WithContext(root))
	if err != nil && strings.Contains(err.Error(), "index.html") {
		t.Fatalf("a built target read the caller's directory: %v", err)
	}

	if err != nil {
		t.Fatal(err)
	}
}

// A context node remembers which directory it came from.
//
// A build has one `-dir`, and an Earthfile referred to across directories has
// its own: `../js+build` copies `index.js` from beside *that* Earthfile. The
// executor was joining every context path to the invocation's directory, so a
// referenced target read files from the caller's tree - and the failure landed
// at execution, after a plan that was entirely correct.
//
// Carried in Meta rather than in the operation, because identity is the file's
// *content*: two identical files in different directories are the same layer
// and should stay one.
func TestAContextNodeRemembersItsDirectory(t *testing.T) {
	t.Parallel()

	root := monorepo(t)

	src := `VERSION 0.8
site:
    FROM alpine:3.22
    COPY ./html+html/index.html ./
`

	err := os.WriteFile(filepath.Join(root, testEarthfile), []byte(src), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	p, err := interp.Build(src, "site", interp.WithContext(root))
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpLocal {
			continue
		}

		// Resolved on both sides: on macOS /var is a symlink to /private/var,
		// and comparing a resolved path with an unresolved one is the bug this
		// package's own unpacker had.
		want, err := filepath.EvalSymlinks(filepath.Join(root, "html"))
		if err != nil {
			t.Fatal(err)
		}

		got, err := filepath.EvalSymlinks(n.Meta.ContextRoot)
		if err != nil {
			t.Fatalf("the context root is not a directory: %v", err)
		}

		if got != want {
			t.Errorf("the context reads from %q, want %q", got, want)
		}

		return
	}

	t.Errorf("no context in the plan:\n%s", describe(p.Graph.Nodes()))
}
