package cli

import "testing"

// TestAPatternArtifactExportsIntoTheDirectory.
//
// `SAVE ARTIFACT ./*` is recorded with the path `/test/*`, and the destination
// for a produced Dockerfile was `filepath.Base` of that - so the export landed
// in a directory literally named `*`, and the reader looking for
// `<tmp>/Dockerfile` found nothing. Both `FROM DOCKERFILE +target/` corpus
// files fail exactly there (gen-dockerfile, from-dockerfile-arg).
//
// A pattern already stages into a directory of its own, holding each match
// under its own name, so the whole of the fix is to copy that directory *as*
// the destination rather than into a subdirectory of it.
func TestAPatternArtifactExportsIntoTheDirectory(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ path, want string }{
		{"/test/*", "/tmp/into"},
		{"/test/dist/*", "/tmp/into"},
		{"/test/f?.txt", "/tmp/into"},
		{"/test/[ab].txt", "/tmp/into"},
		// A plain path still lands under its own name, which is what the reader
		// asks for by base name.
		{"/test/Dockerfile", "/tmp/into/Dockerfile"},
		{"/test/dist/other.Dockerfile", "/tmp/into/other.Dockerfile"},
	} {
		if got := dockerfileDest("/tmp/into", c.path); got != c.want {
			t.Errorf("dockerfileDest(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}
