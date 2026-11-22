package exec

import (
	"os"
	"path/filepath"
	"testing"
)

// TestALocallyCopyLandsWhereCopyWouldPutIt.
//
// The placement rules are a container COPY's, applied on this machine: a
// directory's contents go into the destination unless `--dir` asks for the
// directory itself, and a file goes *into* a destination that is a directory -
// one ending in `/`, or one already there - and *onto* it otherwise.
func TestALocallyCopyLandsWhereCopyWouldPutIt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		dir      bool // the artifact is a directory holding f
		dirCopy  bool
		dest     string
		existing bool // dest exists as a directory
		want     string
	}{
		{"a file onto a name", false, false, "out", false, "out"},
		{"a file into a slash", false, false, "out/", false, "out/a"},
		{"a file into a directory that is there", false, false, "out", true, "out/a"},
		{"a directory's contents", true, false, "out", false, "out/f"},
		{"a directory itself", true, true, "out", false, "out/a/f"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tmp := t.TempDir()
			staged := filepath.Join(tmp, "stage", "a")

			file := staged
			if tc.dir {
				file = filepath.Join(staged, "f")
			}

			err := os.MkdirAll(filepath.Dir(file), 0o750)
			if err == nil {
				err = os.WriteFile(file, []byte("x"), 0o600)
			}

			if err == nil && tc.existing {
				err = os.MkdirAll(filepath.Join(tmp, tc.dest), 0o750)
			}

			if err != nil {
				t.Fatal(err)
			}

			err = placeHostCopy(staged, filepath.Join(tmp, tc.dest)+suffix(tc.dest), tc.dirCopy)
			if err != nil {
				t.Fatal(err)
			}

			b, err := os.ReadFile(filepath.Join(tmp, tc.want))
			if err != nil || string(b) != "x" {
				t.Errorf("nothing at %s (%v)", tc.want, err)
			}
		})
	}
}

// An existing file is replaced, not written over in place: four bytes onto
// eight must leave four (`tests/local+test-local-overwrites`).
func TestALocallyCopyReplacesAFile(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	staged, dest := filepath.Join(tmp, "four"), filepath.Join(tmp, "eight")

	err := os.WriteFile(staged, []byte("4321"), 0o600)
	if err == nil {
		err = os.WriteFile(dest, []byte("12345678"), 0o600)
	}

	if err == nil {
		err = placeHostCopy(staged, dest, false)
	}

	if err != nil {
		t.Fatal(err)
	}

	if b, _ := os.ReadFile(dest); string(b) != "4321" {
		t.Errorf("the destination holds %q, want 4321", b)
	}
}

// suffix keeps a trailing slash that filepath.Join removes.
func suffix(dest string) string {
	if dest[len(dest)-1] == '/' {
		return "/"
	}

	return ""
}
