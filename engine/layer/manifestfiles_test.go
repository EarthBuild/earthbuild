package layer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A manifest hands back the content digest it already recorded.
//
// **The point of writing one down.** A reader that has the manifest knows what
// every regular file in the layer holds without opening any of them, which is
// what lets `COPY --sync` decide a file is unchanged without reading 4 GB to
// prove it.
func TestAManifestHandsBackTheContentDigestsItRecorded(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	writeManifestFixture(t, filepath.Join(root, "one.txt"), "hello")
	writeManifestFixture(t, filepath.Join(root, "two.txt"), "hello")
	writeManifestFixture(t, filepath.Join(root, "other.txt"), "world")

	err := os.Mkdir(filepath.Join(root, "d"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink("one.txt", filepath.Join(root, "link"))
	if err != nil {
		t.Fatal(err)
	}

	m, err := layer.Manifest(root)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}

	files, err := layer.Files(m)
	if err != nil {
		t.Fatalf("files: %v", err)
	}

	one, ok := files["one.txt"]
	if !ok {
		t.Fatalf("one.txt is not in %v", fileNames(files))
	}

	if one.Size != int64(len("hello")) {
		t.Errorf("one.txt is %d bytes, the manifest says %d", len("hello"), one.Size)
	}

	if files["two.txt"].Content != one.Content {
		t.Error("two files holding the same bytes got different digests")
	}

	if files["other.txt"].Content == one.Content {
		t.Error("two files holding different bytes got the same digest")
	}

	// Only regular files have contents, and a caller comparing digests must not
	// be handed a zero one for a directory or a link and take it for an answer.
	for _, p := range []string{"d", "link"} {
		if _, ok := files[p]; ok {
			t.Errorf("%s is not a regular file and has a content digest", p)
		}
	}
}

// A manifest that is not one is refused rather than read as an empty layer.
func TestFilesRefusesBytesThatAreNotAManifest(t *testing.T) {
	t.Parallel()

	_, err := layer.Files([]byte("not a manifest"))
	if err == nil {
		t.Fatal("arbitrary bytes were read as a manifest")
	}
}

func writeManifestFixture(t *testing.T, at, what string) {
	t.Helper()

	err := os.WriteFile(at, []byte(what), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

func fileNames(m map[string]layer.File) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}

// A capture that hands back its manifest hands back the same bytes `Manifest`
// would produce for that tree, ownership declaration and all. Two answers about
// what a layer contains is one too many.
func TestAnOwnedCaptureHandsBackTheSameManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	writeManifestFixture(t, filepath.Join(root, "a.txt"), "hello")
	writeManifestFixture(t, filepath.Join(root, "b.txt"), "world")

	want, err := layer.Manifest(root)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}

	c, got, err := layer.TakeOwnedKnowingManifested(
		root, layer.IDMap{}, layer.IDMap{}, nil, nil)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	if string(got) != string(want) {
		t.Error("a capture's manifest is not the manifest of the tree it captured")
	}

	if layer.ManifestID(got) != c.ID {
		t.Error("the manifest does not hash to the layer it describes")
	}
}
