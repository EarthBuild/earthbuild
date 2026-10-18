package cli

import (
	"path/filepath"
	"testing"
)

// A local destination written as a directory receives the artifact inside it.
//
// `SAVE ARTIFACT ./package.json package.json AS LOCAL ./` means "put it here",
// and writing the artifact *as* `./` failed with "is a directory". A trailing
// separator, `.` or `..` names somewhere to put a thing.
//
// **What is already on disk does not decide it.** This used to join the name
// onto any destination that already existed as a directory, `cp -r` style, so
// `SAVE ARTIFACT /dist AS LOCAL dist` landed at `dist` once and `dist/dist` on
// every run after - the second build of a checkout wrote somewhere the first
// had not. The reference, measured on both engines: without a trailing
// separator the destination is the artifact's new name, and replaces whatever
// was there, directory or not.
func TestALocalDestinationThatIsADirectoryTakesTheName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for _, tc := range []struct{ dest, name, want string }{
		{"./", testManifest, testManifest},
		{".", testManifest, testManifest},
		{"out/", testJar, filepath.Join("out", testJar)},
		{"build/app-renamed.jar", testJar, filepath.Join("build", "app-renamed.jar")},
		{dir, testJar, dir},
		{dir + "/", testJar, filepath.Join(dir, testJar)},
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
