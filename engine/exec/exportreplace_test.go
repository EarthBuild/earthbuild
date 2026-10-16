package exec

import (
	"os"
	"path/filepath"
	"testing"
)

// An artifact exported over what a previous build left replaces it.
//
// **A directory the source no longer has files in keeps none of them.** The
// reference writes `SAVE ARTIFACT /a AS LOCAL out` as `out` itself, so a build
// whose `/a` lost a file leaves `out` without it. Merging kept every file any
// earlier build ever exported there - a stale binary beside the new one, read
// by whatever globbed the directory.
func TestAnExportReplacesWhatWasAtItsDestination(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	dst := filepath.Join(project, "out")

	err := os.MkdirAll(dst, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(dst, "stale"), []byte("old\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = clearForExport(project, dst)
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Lstat(dst)
	if !os.IsNotExist(err) {
		t.Errorf("%s is still there after clearing it for an export (%v)", dst, err)
	}

	// Absent is the first build, and nothing to do.
	err = clearForExport(project, filepath.Join(project, "never-written"))
	if err != nil {
		t.Errorf("clearing an absent destination: %v", err)
	}
}

// But never the project, nor anything holding it.
//
// `AS LOCAL .` for an artifact with no name of its own, or a `..` that climbs
// out, would otherwise name a directory the author's work lives in. Refused
// and left alone, whatever `--force` allowed the write to reach.
func TestAnExportNeverClearsTheProject(t *testing.T) {
	t.Parallel()

	project := filepath.Join(t.TempDir(), "proj")

	err := os.MkdirAll(project, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	for _, dst := range []string{project, project + "/", filepath.Dir(project)} {
		if clearForExport(project, dst) == nil {
			t.Errorf("clearing %s was allowed; it holds the project", dst)
		}

		if _, err := os.Stat(project); err != nil {
			t.Fatalf("the project is gone after clearing %s: %v", dst, err)
		}
	}
}
