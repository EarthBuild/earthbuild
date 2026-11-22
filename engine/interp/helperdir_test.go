package interp_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A helper path means the directory of the Earthfile that wrote it.
//
// **The same bug as `TestAReferencedTargetReadsItsOwnDirectory`, one construct
// over.** `unit.dir` is documented as "this Earthfile's directory: its build
// context, and the root that its relative references are resolved against", and
// `--helper` was resolved against the *invocation's* directory instead - so
// `--helper ./h.wasm` in `examples/npm/Earthfile` meant a file at the repository
// root when the build was started there, and meant the right file only when it
// happened to be started in that subdirectory.
//
// Which is how every example in this repository is built: `BUILD ./examples/x+y`
// from the root Earthfile. The construct would have been unusable in exactly the
// place it is meant to be shown off, and the symptom is a cache that quietly
// does not share.
func TestAHelperPathMeansItsOwnEarthfilesDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sub := filepath.Join(root, "npm")

	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(sub, testEarthfile), `VERSION 0.8
deps:
    FROM alpine:3.22
    CACHE --id c --portable-except '' --helper ./h.wasm /c
    RUN echo hi
`)

	src := `VERSION 0.8
all:
    BUILD ./npm+deps
`
	write(t, filepath.Join(root, testEarthfile), src)

	var asked []string

	_, err := interp.Build(src, "all",
		interp.WithContext(root),
		interp.WithHelperResolver(func(ref, dir string) (string, error) {
			asked = append(asked, filepath.Join(dir, ref))

			return ir.DigestOf([]byte("a module")).String(), nil
		}))
	if err != nil {
		t.Fatal(err)
	}

	if len(asked) != 1 {
		t.Fatalf("the resolver was asked %d times, want once", len(asked))
	}

	// Both sides through EvalSymlinks: on macOS `t.TempDir()` hands back a path
	// under `/var`, which is a symlink to `/private/var`, and the interpreter
	// reports the resolved one. Comparing them raw fails for a reason that has
	// nothing to do with what is under test.
	if want := real(t, filepath.Join(sub, "h.wasm")); real(t, asked[0]) != want {
		t.Errorf("looked for the helper at %s\n  want %s"+
			"\n  a path in a sub-Earthfile means the directory that Earthfile is in,"+
			" which is what every example in this repository relies on", asked[0], want)
	}
}

// real is a path with every symlink resolved, so two spellings of one file
// compare equal.
func real(t *testing.T, at string) string {
	t.Helper()

	dir, err := filepath.EvalSymlinks(filepath.Dir(at))
	if err != nil {
		return at
	}

	return filepath.Join(dir, filepath.Base(at))
}

func write(t *testing.T, at, body string) {
	t.Helper()

	if err := os.WriteFile(at, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
