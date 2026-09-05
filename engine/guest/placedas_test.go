package guest

import "testing"

// TestACopyLandsUnderTheNameItWasAskedFor.
//
// A copy into a directory lands under the source's base name, which is right
// for a path and wrong for an artifact that was *renamed*:
//
//	SAVE ARTIFACT ./file.txt ./yet-another-file-with-+.txt
//
// stores the bytes at /test/file.txt under the name
// /yet-another-file-with-+.txt, and `COPY +t/yet-another-file-with-\+.txt ./`
// put `file.txt` in the step - so the `cat` on the next line of
// tests/escape.earth read a file that was not there.
//
// The stored path is what the guest can see; the name is what the reference
// asked for, so it has to travel with the request.
func TestACopyLandsUnderTheNameItWasAskedFor(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ src, as, want string }{
		{"/test/file.txt", "yet-another-file-with-+.txt", "yet-another-file-with-+.txt"},
		// No name asked for is every ordinary copy, and is the base name.
		{"/test/file.txt", "", "file.txt"},
		{"/a/b/c", "", "c"},
		// A name that happens to match changes nothing.
		{"/test/file.txt", "file.txt", "file.txt"},
	} {
		if got := placedAs(c.src, c.as); got != c.want {
			t.Errorf("placedAs(%q, %q) = %q, want %q", c.src, c.as, got, c.want)
		}
	}
}
