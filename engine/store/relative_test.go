package store

import "testing"

// A path that tries to leave the layer is contained rather than refused.
//
// **The guard that used to be here never fired.** `relative` returned a bool the
// doc described as refusing an escaping path, and it was `true` on every path
// through the function - so a caller reading the signature believed in a check
// that did not exist (unparam found it; E625).
//
// What actually holds is stronger and worth writing down: `filepath.Clean("/" +
// p)` resolves `..` against the root it has just prefixed, so an escape is
// normalised into the tree instead of out of it. That is the property the view
// depends on, and until now nothing asserted it.
func TestAnEscapingPathIsContained(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"../../etc/passwd":     "etc/passwd",
		"/../../etc/passwd":    "etc/passwd",
		"a/../../../b":         "b",
		"..":                   ".",
		"/":                    ".",
		"":                     ".",
		".":                    ".",
		"usr/lib/libc.so":      "usr/lib/libc.so",
		"/usr/lib/libc.so":     "usr/lib/libc.so",
		"./usr/../usr/lib/x.h": "usr/lib/x.h",
	} {
		if got := relative(in); got != want {
			t.Errorf("relative(%q) = %q, want %q", in, got, want)
		}
	}
}

// And nothing it returns begins with a separator or a parent reference, which is
// what "under a layer root" means when it is joined onto one.
func TestNothingRelativeReturnsCanBeJoinedOutOfATree(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"../../etc/passwd", "/../..", "a/../../b", "..", "/", "", "///x",
	} {
		got := relative(in)

		if got == "" {
			t.Errorf("relative(%q) returned an empty path, which joins to the root itself", in)
		}

		if got[0] == '/' {
			t.Errorf("relative(%q) = %q, which is absolute", in, got)
		}

		if got == ".." || len(got) > 2 && got[:3] == "../" {
			t.Errorf("relative(%q) = %q, which climbs out when joined", in, got)
		}
	}
}
