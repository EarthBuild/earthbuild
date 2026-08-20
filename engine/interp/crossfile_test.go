package interp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// tree writes a set of files and returns the root.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()

	for p, body := range files {
		full := filepath.Join(root, p)
		err := os.MkdirAll(filepath.Dir(full), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(full, []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	return root
}

// buildIn builds a target from the Earthfile in dir.
func buildIn(t *testing.T, dir, target string) (*interp.Plan, error) {
	t.Helper()

	src, err := os.ReadFile(filepath.Join(dir, testEarthfile)) //nolint:gosec // a fixture this test wrote
	if err != nil {
		t.Fatal(err)
	}

	return interp.Build(string(src), target, interp.WithContext(dir))
}

// `./sub+target` builds a target in another Earthfile.
func TestCrossFileTargetReference(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{
		testEarthfile: versioned + `
main:
    FROM alpine:3.22
    BUILD ./lib+build
`,
		testLibEarthfile: versioned + `
build:
    FROM alpine:3.22
    RUN make-lib
`,
	})

	p, err := buildIn(t, root, testMain)
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "make-lib") {
		t.Errorf("the other Earthfile's target was not built:\n%s", got)
	}
}

// A COPY inside the other Earthfile resolves against **its** directory.
//
// This is the trap: reading `src/main.go` relative to the calling Earthfile
// would silently copy a different file, or fail claiming a file is missing that
// is sitting exactly where its own Earthfile says it should be.
func TestEachEarthfileHasItsOwnContext(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{
		testEarthfile: versioned + `
main:
    FROM alpine:3.22
    BUILD ./lib+build
`,
		testLibEarthfile: versioned + `
build:
    FROM alpine:3.22
    COPY only-in-lib /app/
`,
		"lib/only-in-lib": "lib content",
	})

	_, err := buildIn(t, root, testMain)
	if err != nil {
		t.Fatalf("a COPY was resolved against the wrong directory: %v", err)
	}
}

// `../..+target` walks upwards, which is how a test directory refers to the
// repository root.
func TestUpwardCrossFileReference(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{
		testEarthfile: versioned + `
root-target:
    FROM alpine:3.22
    RUN root-thing
`,
		"tests/Earthfile": versioned + `
run:
    FROM alpine:3.22
    BUILD ..+root-target
`,
	})

	p, err := buildIn(t, filepath.Join(root, "tests"), "run")
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "root-thing") {
		t.Errorf("the parent Earthfile's target was not built:\n%s", got)
	}
}

// FROM and COPY take cross-file references too.
func TestCrossFileFromAndArtifactCopy(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{
		testEarthfile: versioned + `
main:
    FROM ./lib+lib-base
    COPY ./lib+build/out /app/
`,
		testLibEarthfile: versioned + `
lib-base:
    FROM alpine:3.22
    RUN lib-base-step

build:
    FROM alpine:3.22
    RUN lib-build
    SAVE ARTIFACT /out
`,
	})

	p, err := buildIn(t, root, testMain)
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	for _, want := range []string{"lib-base-step", "lib-build"} {
		if !strings.Contains(got, want) {
			t.Errorf("the graph is missing %q:\n%s", want, got)
		}
	}
}

// An Earthfile that is not there says where it looked.
func TestMissingEarthfileSaysWhereItLooked2(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{
		testEarthfile: versioned + "\nmain:\n    FROM alpine\n    BUILD ./missing+build\n",
	})

	_, err := buildIn(t, root, testMain)
	if err == nil {
		t.Fatal("a reference to a missing Earthfile was accepted")
	}

	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("the error does not name the path:\n%s", err)
	}
}

// A remote reference needs the network and a checkout, and is refused rather
// than guessed at.
func TestRemoteReferencesAreRefused(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{
		testEarthfile: versioned + "\nmain:\n    FROM alpine\n    BUILD github.com/org/repo+build\n",
	})

	_, err := buildIn(t, root, testMain)
	if err == nil {
		t.Fatal("a remote target reference was accepted")
	}

	if !strings.Contains(err.Error(), testRepo) {
		t.Errorf("the refusal does not quote the reference:\n%s", err)
	}
}

// A cycle across files is still a cycle.
func TestCrossFileCyclesAreRefused(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{
		testEarthfile:    versioned + "\nmain:\n    FROM ./lib+build\n",
		testLibEarthfile: versioned + "\nbuild:\n    FROM ..+main\n",
	})

	_, err := buildIn(t, root, testMain)
	if err == nil {
		t.Fatal("a cycle across two Earthfiles was accepted")
	}

	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("the error does not name the cycle:\n%s", err)
	}
}
