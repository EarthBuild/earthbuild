package interp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A helper that is an artifact of a target in another Earthfile.
//
// **`TestAHelperPathMeansItsOwnEarthfilesDirectory`'s bug, one form over.** A
// helper *path* resolves against `unit.dir`; a helper *target reference* did not
// resolve at all. The builder runs outside the interpreter and so cannot know
// which Earthfile wrote a reference - `../../..+cache-helper` reached it
// verbatim, was looked up as a target of the *entry* Earthfile, and came back
// "no such target". Reported, per I11, as a cache that quietly does not share:
//
//	note: ../../..+cache-helper was not built, so the cache it reads is not
//	shared: planning ../../..+cache-helper (Earthfile:14): no such target
//
// Which is every example in `examples/cache-helpers`, each of which names a
// helper built by the repository root's `+cache-helper`.
//
// So the directory part is made absolute before it crosses the seam. A caller
// outside the interpreter has no base to resolve one against, and inventing one
// from its own working directory is what produced the bug above.
func TestAHelperArtifactRefIsResolvedAgainstItsOwnEarthfile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sub := filepath.Join(root, "eg", "go-build")

	err := os.MkdirAll(sub, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	made := t.TempDir()
	module := []byte("a module the root's target produced")

	// Staged under the name the reference asked for, directory and all: a
	// reference is relative to the producing target's working directory, and
	// `+gen/build/h.wasm` is not `+gen/h.wasm`.
	err = os.MkdirAll(filepath.Join(made, "build"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(made, "build", "h.wasm"), module, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(sub, testEarthfile), `VERSION 0.8
compile:
    FROM alpine:3.22
    CACHE --id c --portable-except '' --helper ../../+gen/build/h.wasm /c
    RUN echo hi
`)

	src := `VERSION 0.8
all:
    BUILD ./eg/go-build+compile
`
	write(t, filepath.Join(root, testEarthfile), src)

	var asked []string

	p, err := interp.Build(src, "all",
		interp.WithContext(root),
		interp.WithArtifacts(func(ref, _ string) (string, error) {
			asked = append(asked, ref)

			return made, nil
		}),
		interp.WithHelperResolver(func(ref, dir string) (string, error) {
			at := ref
			if !filepath.IsAbs(at) {
				at = filepath.Join(dir, ref)
			}

			b, readErr := os.ReadFile(at)
			if readErr != nil {
				return "", readErr
			}

			return ir.DigestOf(b).String(), nil
		}))
	if err != nil {
		t.Fatal(err)
	}

	if len(p.HelperNotes) != 0 {
		t.Fatalf("the helper did not resolve: %v", p.HelperNotes)
	}

	if len(asked) != 1 {
		t.Fatalf("the builder was asked %d times, want once: %v", len(asked), asked)
	}

	// The root's `+gen`, named absolutely: the reference is written two
	// directories down and the builder has no way to know that.
	dir, name, _ := strings.Cut(asked[0], "+")
	if real(t, dir) != real(t, root) || name != "gen/build/h.wasm" {
		t.Errorf("the builder was asked for %q\n  want %s+gen/build/h.wasm"+
			"\n  a target reference in a sub-Earthfile means that Earthfile's"+
			" directory, and the builder cannot know which one that is",
			asked[0], real(t, root))
	}

	m, ok := cacheMountOf(p.Graph.Root)
	if !ok {
		t.Fatal("the plan has no cache mount")
	}

	if want := ir.DigestOf(module).String(); m.HelperID != want {
		t.Errorf("pinned %q, want the digest of what the target produced (%s)", m.HelperID, want)
	}
}
