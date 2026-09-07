package guest

import (
	"os"
	"path/filepath"
	"testing"
)

// A staged export holds what this build produced, and nothing older.
//
// The staging area lives in the store and is named from the request, so two
// builds saving a directory to the same destination stage into the same place -
// and nothing ever emptied it. `copyPath` merges into a directory that is
// already there, so the second build's artifact carried out the first build's
// files, and the third carried both.
//
// Measured against earthly before it was fixed: two unrelated single-file
// builds, in two fresh project directories, each exported the union of both
// plus the leftovers of every earlier build that had used the destination
// `out/d`. The reference produced exactly what each build made.
//
// It is worth being precise about why this is worse than it sounds. The
// contamination crosses *projects*, because the store outlives any one of them;
// the output depends on the store's history rather than on the build, so it is
// not reproducible; and the extra files are another build's, which is a
// disclosure as well as a defect.
func TestAStagedExportDoesNotKeepAnEarlierBuildsFiles(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	s := &Server{LayerDir: store}

	stage := func(t *testing.T, name string) {
		t.Helper()

		root := t.TempDir()

		err := os.MkdirAll(filepath.Join(root, "d"), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(filepath.Join(root, "d", name), []byte(name), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		err = s.export(fixedHandle{root: root}, "d", "out/d", nil, false)
		if err != nil {
			t.Fatal(err)
		}
	}

	stage(t, "first.txt")
	stage(t, "second.txt")

	got, err := os.ReadDir(filepath.Join(store, "exports", "out", "d"))
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(got))
	for _, e := range got {
		names = append(names, e.Name())
	}

	if len(names) != 1 || names[0] != "second.txt" {
		t.Errorf("the second build staged %v, want only its own file: the"+
			" first build's artifact is still in the staging directory and"+
			" would be copied into the second build's output", names)
	}
}
