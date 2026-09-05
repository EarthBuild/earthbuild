package cli

import (
	"path/filepath"
	"testing"
)

// A local destination that is a directory receives the artifact inside it.
//
// `SAVE ARTIFACT ./package.json package.json AS LOCAL ./` means "put it here",
// and writing the artifact *as* `./` failed with "is a directory" - the same
// rule COPY needed, arriving from the other end: a trailing separator, or a
// path that is already a directory, names somewhere to put a thing rather than
// the thing's new name.
func TestALocalDestinationThatIsADirectoryTakesTheName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for _, tc := range []struct{ dest, name, want string }{
		{"./", testManifest, testManifest},
		{".", testManifest, testManifest},
		{"out/", testJar, filepath.Join("out", testJar)},
		{"build/app-renamed.jar", testJar, filepath.Join("build", "app-renamed.jar")},
		{dir, testJar, filepath.Join(dir, testJar)},
	} {
		t.Run(tc.dest, func(t *testing.T) {
			t.Parallel()

			if got := localPath(tc.dest, tc.name); got != tc.want {
				t.Errorf("%q with name %q lands at %q, want %q", tc.dest, tc.name, got, tc.want)
			}
		})
	}
}

// An artifact with no name of its own keeps the destination it was given.
func TestALocalDestinationWithoutANameIsUnchanged(t *testing.T) {
	t.Parallel()

	if got := localPath("build/out.txt", ""); got != "build/out.txt" {
		t.Errorf("the destination became %q", got)
	}
}
