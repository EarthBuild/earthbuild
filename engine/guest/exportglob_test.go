package guest

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExportingAPatternExportsEveryMatch.
//
// `SAVE ARTIFACT ./out-* AS LOCAL ./output/` names files whose count the build
// decides - `tests/build-arg-repeat.earth` writes one per build argument - so
// there is nothing to stat when the plan is made, and the export stat'd the
// pattern itself and reported `no such file or directory` naming a path with a
// star in it.
//
// The consuming side has matched patterns since the beginning: `findInStack`
// does it for exactly this reason, quoted in its own comment. Only the
// producing side did not, which is the shape of divergence this file keeps
// finding - one rule written out twice and maintained once.
//
// Each match keeps its own name below the destination, because a pattern's
// matches are files and the destination is where they go, not what they are
// called.
func TestExportingAPatternExportsEveryMatch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := t.TempDir()

	for _, name := range []string{"out-one", "out-two", "keep-me"} {
		err := os.WriteFile(filepath.Join(root, name), []byte(name+"\n"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	s := &Server{LayerDir: dir}

	err := s.export(fixedHandle{root: root}, "out-*", "output/", nil)
	if err != nil {
		t.Fatalf("a pattern naming two files was refused: %v", err)
	}

	for _, want := range []string{"out-one", "out-two"} {
		body, rerr := os.ReadFile(filepath.Join(dir, "exports", "output", want))
		if rerr != nil {
			t.Errorf("%s was not exported: %v", want, rerr)

			continue
		}

		if string(body) != want+"\n" {
			t.Errorf("%s holds %q", want, body)
		}
	}

	// The pattern selects: a file it does not match stays behind.
	_, err = os.Lstat(filepath.Join(dir, "exports", "output", "keep-me"))
	if err == nil {
		t.Error("keep-me was exported by `out-*`, so the pattern was ignored" +
			" and the whole directory taken")
	}
}
