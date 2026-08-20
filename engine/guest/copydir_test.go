package guest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// copyDirFixture makes a layer holding a directory with one file in it.
func copyDirFixture(t *testing.T) (*Server, fixedHandle) {
	t.Helper()

	dir := t.TempDir()

	layer := filepath.Join(dir, "layers", testSrcLayer, "src")
	err := os.MkdirAll(layer, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(layer, "main.cpp"), []byte("int main(){}\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(dir, "root")
	err = os.MkdirAll(filepath.Join(root, "code"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	return &Server{LayerDir: dir}, fixedHandle{root: root}
}

// `COPY src .` copies what is *in* the directory, not the directory.
//
// Docker's rule and Earthfile's: a directory source without `--dir` contributes
// its contents. `COPY src .` under `WORKDIR /code` puts main.cpp at
// /code/main.cpp, and the build that follows says `gcc -c main.cpp`.
//
// This engine put it at /code/src/main.cpp, because the destination had a
// trailing separator and the guest read that as "place the source inside" - a
// rule that is right for a *file* and wrong for a directory. The separator was
// carrying two meanings.
func TestADirectorySourceContributesItsContents(t *testing.T) {
	t.Parallel()

	s, h := copyDirFixture(t)

	err := s.copyIn(h, []string{testSrcLayer}, "src", "/code/", copyOpts{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(filepath.Join(h.root, "code", "main.cpp"))
	if err != nil {
		t.Errorf("the directory's contents are not in the destination: %v", err)
	}

	_, err = os.Stat(filepath.Join(h.root, "code", "src"))
	if err == nil {
		t.Error("the directory itself was copied, which is what --dir asks for")
	}
}

// `COPY --dir src .` copies the directory itself.
func TestDirCopiesTheDirectoryItself(t *testing.T) {
	t.Parallel()

	s, h := copyDirFixture(t)

	err := s.copyIn(h, []string{testSrcLayer}, "src", "/code/", copyOpts{AsDir: true})
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(filepath.Join(h.root, "code", "src", "main.cpp"))
	if err != nil {
		t.Errorf("--dir did not copy the directory: %v", err)
	}
}

// `--dir` with no destination to go inside *becomes* the destination.
//
// `COPY --dir tree /placed` gives /placed/inner.txt when /placed does not
// exist, and /placed/tree/inner.txt when it does. It is `cp -r`, and nothing is
// placed inside a destination that is not already a directory.
//
// This test asserted the opposite until the reference was asked across all four
// combinations of the flag and an existing destination (E48). It was written
// from the same misreading as the code, which is why it agreed with it.
func TestDirWithNoDestinationBecomesTheDestination(t *testing.T) {
	t.Parallel()

	s, h := copyDirFixture(t)

	err := s.copyIn(h, []string{testSrcLayer}, "src", "/placed", copyOpts{AsDir: true})
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(filepath.Join(h.root, "placed", "main.cpp"))
	if err != nil {
		t.Errorf("--dir did not become the destination that was not there: %v", err)
	}
}

// And with a destination that is already a directory, the name comes along.
//
// The other half, written beside the first because each is what makes the other
// look wrong. `/code` exists in the fixture, so `COPY --dir src /code` places
// /code/src - which is the reading that was applied unconditionally before.
func TestDirPlacesTheDirectoryInsideOneThatExists(t *testing.T) {
	t.Parallel()

	s, h := copyDirFixture(t)

	err := s.copyIn(h, []string{testSrcLayer}, "src", "/code", copyOpts{AsDir: true})
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(filepath.Join(h.root, "code", "src", "main.cpp"))
	if err != nil {
		t.Errorf("--dir did not place the directory inside an existing one: %v", err)
	}
}

// A file source is unaffected: it still lands inside a directory destination.
func TestAFileStillLandsInsideADirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	layer := filepath.Join(dir, "layers", testSrcLayer)
	err := os.MkdirAll(layer, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(layer, "one.txt"), []byte("1\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(dir, "root")
	err = os.MkdirAll(filepath.Join(root, "code"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	s, h := &Server{LayerDir: dir}, fixedHandle{root: root}

	err = s.copyIn(h, []string{testSrcLayer}, "one.txt", "/code/", copyOpts{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(filepath.Join(h.root, "code", "one.txt"))
	if err != nil {
		t.Errorf("a file did not land inside the directory: %v", err)
	}
}

var _ = core.Observation{}

// A source may be a pattern, and is matched against the layer it comes from.
//
// `SAVE ARTIFACT target/uberjar/*-standalone.jar` names a file whose version is
// decided by the build that made it, so the pattern cannot be resolved when the
// plan is made - only against the filesystem that has it. Passing it through
// unmatched asked for a file with a `*` in its name.
func TestAPatternSourceIsMatchedInTheLayer(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	layer := filepath.Join(dir, "layers", testSrcLayer, "out")
	err := os.MkdirAll(layer, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(layer, "app-1.2.3-standalone.jar"), []byte("jar\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(dir, "root")
	err = os.MkdirAll(root, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	s, h := &Server{LayerDir: dir}, fixedHandle{root: root}

	err = s.copyIn(h, []string{testSrcLayer}, "out/*-standalone.jar", "/app.jar", copyOpts{})
	if err != nil {
		t.Fatalf("a pattern source was not matched: %v", err)
	}

	_, err = os.Stat(filepath.Join(root, "app.jar"))
	if err != nil {
		t.Errorf("the matched file did not arrive: %v", err)
	}
}

// A pattern matching nothing says so, naming the pattern.
func TestAPatternMatchingNothingIsNamed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.MkdirAll(filepath.Join(dir, "layers", testSrcLayer), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(dir, "root")
	err = os.MkdirAll(root, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	s, h := &Server{LayerDir: dir}, fixedHandle{root: root}

	err = s.copyIn(h, []string{testSrcLayer}, "out/*.jar", "/app.jar", copyOpts{})
	if err == nil {
		t.Fatal("a pattern that matches nothing was accepted")
	}

	if !strings.Contains(err.Error(), "*.jar") {
		t.Errorf("the refusal does not name the pattern: %v", err)
	}
}

// A copy searches the whole stack the artifact came from, not one layer.
//
// An artifact need not be made by its target's *last* step: clojure's build
// runs `lein uberjar`, then extracts a version from the jar, then saves the
// jar - so the jar is two layers down. Reading only the producing node's own
// layer found nothing and said the pattern matched nothing, which is true of
// that layer and false of the target.
//
// Searched newest first, because a later layer replacing a file is the later
// file.
func TestACopySearchesTheWholeStack(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// The jar is in the older layer; the newer one holds only the version file.
	older := filepath.Join(dir, "layers", testOlder)
	newer := filepath.Join(dir, "layers", testNewer)

	for _, d := range []string{older, newer} {
		err := os.MkdirAll(d, 0o750)
		if err != nil {
			t.Fatal(err)
		}
	}

	err := os.WriteFile(filepath.Join(older, "app.jar"), []byte("jar\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(newer, "version"), []byte("1.0\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(dir, "root")
	err = os.MkdirAll(root, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	s, h := &Server{LayerDir: dir}, fixedHandle{root: root}

	err = s.copyIn(h, []string{testOlder, testNewer}, "app.jar", "/app.jar", copyOpts{})
	if err != nil {
		t.Fatalf("the artifact was not found in the stack: %v", err)
	}

	_, err = os.Stat(filepath.Join(root, "app.jar"))
	if err != nil {
		t.Errorf("the file from the older layer did not arrive: %v", err)
	}
}

// The newest layer holding the path is the one taken.
func TestACopyTakesTheNewestLayersVersion(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for name, body := range map[string]string{testOlder: "old\n", testNewer: "new\n"} {
		d := filepath.Join(dir, "layers", name)
		err := os.MkdirAll(d, 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(filepath.Join(d, "f"), []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	root := filepath.Join(dir, "root")
	err := os.MkdirAll(root, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	s, h := &Server{LayerDir: dir}, fixedHandle{root: root}

	err = s.copyIn(h, []string{testOlder, testNewer}, "f", "/f", copyOpts{})
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(root, "f")) //nolint:gosec // a fixture this test wrote
	if err != nil {
		t.Fatal(err)
	}

	if string(b) != "new\n" {
		t.Errorf("took %q, want the newer layer's", b)
	}
}
