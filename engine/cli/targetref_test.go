package cli

import "testing"

// TestATargetMayNameTheDirectoryItLivesIn.
//
// **`./dir+target` is how the language refers to a target elsewhere**, and the
// interpreter has resolved it since the beginning - `targetRef` does it for
// every `BUILD`, `COPY` and `FROM` that crosses an Earthfile. The command line
// did not, so a reference that is ordinary inside a build was refused at the
// front door:
//
//	no target named "./autocompletion+test-all"
//
// It is the form this repository's own corpus uses throughout, so nothing in
// `tests/` could be driven by naming it.
//
// The directory becomes the build's directory, because that is what it means: a
// target is read from the Earthfile beside it and its context is its own
// directory, not the caller's.
func TestATargetMayNameTheDirectoryItLivesIn(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		dir, ref       string
		wantDir, wantT string
	}{
		// The plain form is unchanged: no path, so the directory stands.
		{"/w", "+build", "/w", "build"},
		{"/w", "build", "/w", "build"},

		// A relative directory, resolved against the one given.
		{"/w", "./sub+build", "/w/sub", "build"},
		{"/w", "sub+build", "/w/sub", "build"},
		{"/w", "./a/b+build", "/w/a/b", "build"},

		// `..` is a legal way to name a sibling and must not be flattened away.
		{"/w/x", "../y+build", "/w/y", "build"},

		// An absolute path replaces the directory rather than joining to it.
		{"/w", "/abs+build", "/abs", "build"},

		// Not a reference at all: no `+`, so nothing is split.
		{"/w", "ls", "/w", "ls"},
	} {
		gotDir, gotT := splitTargetRef(c.dir, c.ref)
		if gotDir != c.wantDir || gotT != c.wantT {
			t.Errorf("splitTargetRef(%q, %q) = (%q, %q), want (%q, %q)",
				c.dir, c.ref, gotDir, gotT, c.wantDir, c.wantT)
		}
	}
}
