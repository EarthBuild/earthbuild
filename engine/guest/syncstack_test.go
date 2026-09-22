package guest

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// `--sync` of a directory several layers built keeps what every layer put there.
//
// **Pruned against the stack, not against each layer.** A directory the source
// target wrote in three `COPY`s is three layers each holding one entry, plus a
// `RUN` that touched it and holds none. The copy walks them oldest first, and
// each pass used to prune the destination to match *its* layer - so every pass
// deleted what the one before had placed, and `COPY --sync --dir +src/w /`
// landed only the last entry, or nothing when the newest layer was the `RUN`.
//
// The stale entry in the base is the other half: it must still go, or the fix
// is just "stop pruning".
func TestSyncOfADirectoryBuiltAcrossLayersKeepsEveryLayer(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	layers := map[string][]string{
		"l1": {"w/Cargo.lock"},
		"l2": {"w/d1/f"},
		"l3": {"w/sub/Cargo.toml"},
		"l4": {}, // a RUN that touched /w and left nothing new in it
	}

	for id, files := range layers {
		err := os.MkdirAll(filepath.Join(dir, "layers", id, "w"), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		for _, f := range files {
			p := filepath.Join(dir, "layers", id, f)

			err = os.MkdirAll(filepath.Dir(p), 0o750)
			if err != nil {
				t.Fatal(err)
			}

			err = os.WriteFile(p, []byte(f+"\n"), 0o600)
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	root := filepath.Join(dir, "root")

	err := os.MkdirAll(filepath.Join(root, "w"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(root, "w", "stale"), []byte("gone\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{LayerDir: dir}

	err = s.copyIn(fixedHandle{root: root}, []string{"l1", "l2", "l3", "l4"}, "/w", "/",
		copyOpts{AsDir: true, Sync: true})
	if err != nil {
		t.Fatal(err)
	}

	var got []string

	err = filepath.WalkDir(filepath.Join(root, "w"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		rel, err := filepath.Rel(root, p)
		got = append(got, filepath.ToSlash(rel))

		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"w/Cargo.lock", "w/d1/f", "w/sub/Cargo.toml"}
	if !slices.Equal(got, want) {
		t.Errorf("/w holds %q, want %q"+
			"\n  each layer the source was built in contributes to the directory;"+
			"\n  pruning against one of them deletes what the others placed", got, want)
	}
}
