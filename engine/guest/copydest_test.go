package guest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// fixedHandle is a materialised filesystem that is just a directory.
type fixedHandle struct{ root string }

func (h fixedHandle) Root() string { return h.root }

func (h fixedHandle) Observations() core.Observation { return core.Observation{} }

func (h fixedHandle) Delta() string { return h.root }
func (h fixedHandle) Release() error {
	return nil
}

// copyFixture makes a layer store holding one file and a destination root.
func copyFixture(t *testing.T) (s *Server, h fixedHandle) {
	t.Helper()

	dir := t.TempDir()

	layer := filepath.Join(dir, "layers", testSrcLayer)
	err := os.MkdirAll(layer, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(layer, "src.txt"), []byte("hi\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(dir, "root")
	err = os.MkdirAll(filepath.Join(root, "w"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	return &Server{LayerDir: dir}, fixedHandle{root: root}
}

// A destination that is an existing directory takes the source *inside* it.
//
// The rule was "ends in a separator", which is true of `/app/` and not of `.`
// or of `/app` when /app already exists - and `COPY x .` is how nearly every
// Earthfile and Dockerfile in the world writes a copy. Writing the file *as*
// the directory is not a subtle failure: it fails outright with "is a
// directory", from inside the guest, naming an overlay path the author has
// never heard of.
func TestACopyIntoAnExistingDirectoryLandsInside(t *testing.T) {
	t.Parallel()

	for _, dest := range []string{".", "./", "/w", "/w/", "w"} {
		t.Run(dest, func(t *testing.T) {
			t.Parallel()
			s, h := copyFixture(t)

			err := s.copyIn(h, []string{testSrcLayer}, "src.txt", dest, copyOpts{})
			if err != nil {
				t.Fatalf("COPY src.txt %s: %v", dest, err)
			}

			want := filepath.Join(h.root, filepath.Clean(dest), "src.txt")
			if filepath.Clean(dest) == "." {
				want = filepath.Join(h.root, "src.txt")
			}

			b, err := os.ReadFile(want) //nolint:gosec // a fixture this test wrote
			if err != nil {
				t.Fatalf("the file is not at %s: %v", want, err)
			}

			if string(b) != "hi\n" {
				t.Errorf("the destination holds %q", b)
			}
		})
	}
}

// A destination that does not exist is the new name of the file.
//
// The other half of the same rule, and the reason it cannot simply always place
// the source inside: `COPY src.txt config.json` renames.
func TestACopyToANameThatDoesNotExistRenames(t *testing.T) {
	t.Parallel()

	s, h := copyFixture(t)

	err := s.copyIn(h, []string{testSrcLayer}, "src.txt", "/w/renamed.txt", copyOpts{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(filepath.Join(h.root, "w", "renamed.txt"))
	if err != nil {
		t.Errorf("the file was not renamed: %v", err)
	}

	_, err = os.Stat(filepath.Join(h.root, "w", "renamed.txt", "src.txt"))
	if err == nil {
		t.Error("the file was placed inside a directory named after the destination")
	}
}
