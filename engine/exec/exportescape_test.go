package exec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An artifact cannot be written outside the project, whatever it was told.
//
// `SAVE ARTIFACT x AS LOCAL <dest>` lets an Earthfile choose where to write on
// the machine running the build, and an Earthfile is routinely somebody else's
// code fetched from somewhere else. The interpreter already refuses an absolute
// path, a `~`, and anything climbing out with `..`.
//
// This is the second check, at the layer that does the writing - the same shape
// as `within()` in the git fetcher, and for the same reason: the check that
// matters is the one next to the damage. A refusal in the interpreter protects
// every caller that goes through the interpreter, and this engine is a library.
func TestAnExportCannotLeaveTheProject(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	for _, tc := range []struct {
		name string
		dest string
		ok   bool
	}{
		{"a file in the project", "out.txt", true},
		{"a file below it", "dist/out.txt", true},
		{"a path that climbs out", filepath.Join("..", "escaped.txt"), false},
		{"a path that climbs out and back", filepath.Join("..", "..", "etc", "passwd"), false},
		{"an absolute path", "/etc/passwd", false},
		{
			// Clean() alone does not settle this: the string does not climb,
			// but the directory it names is not below the root either.
			name: "a sibling reached the long way",
			dest: filepath.Join("sub", "..", "..", "sibling", "out.txt"),
			ok:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := insideProject(root, tc.dest)
			if tc.ok && err != nil {
				t.Errorf("%q was refused: %v", tc.dest, err)
			}

			if !tc.ok && err == nil {
				t.Errorf("%q was allowed to leave the project", tc.dest)
			}

			if !tc.ok && err != nil && !strings.Contains(err.Error(), tc.dest) {
				t.Errorf("the refusal does not name the destination:\n%v", err)
			}
		})
	}
}

// With no project to be inside, nothing is refused.
//
// A caller that set no context has not told this layer what "outside" means,
// and inventing an answer would refuse exports that a library user arranged
// perfectly well for themselves.
func TestAnExportWithNoProjectIsNotRefused(t *testing.T) {
	t.Parallel()

	err := insideProject("", filepath.Join("..", "anywhere.txt"))
	if err != nil {
		t.Errorf("a destination was refused with no project set: %v", err)
	}
}

// A symlink in the project cannot be used to write outside it.
//
// This is the case the string checks cannot see and the reason this layer
// resolves rather than cleans. `dist/out.txt` is a relative path that does not
// climb, so the interpreter passes it - correctly, because nothing about the
// text is wrong. What is wrong is `dist`, which is a link to somewhere else,
// and only the filesystem knows that.
//
// It is not hypothetical for a build tool: the project directory is checked out
// from wherever the Earthfile came from, so whoever wrote the Earthfile may
// also have written the symlink.
func TestASymlinkCannotBeUsedToEscapeTheProject(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()

	err := os.Symlink(outside, filepath.Join(root, "dist"))
	if err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	err = insideProject(root, filepath.Join("dist", "out.txt"))
	if err == nil {
		t.Error("a symlink out of the project was accepted as a destination")
	}
}
