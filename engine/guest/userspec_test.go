package guest

import "testing"

// A USER spec names a user, a group, or both, by name or by number.
//
// **`USER` was recorded and never applied.** The interpreter carries it, the
// key hashes it, and the step ran as root anyway - so `USER testuser` followed
// by `RUN test -O ./a.txt` failed on a file that testuser owned, because the
// step asking was not testuser. A step that drops privileges is the whole point
// of the instruction, and an engine that silently ignores it runs build code
// with more authority than the Earthfile asked for.
//
// Numbers are resolved here; names need the step's own `/etc/passwd` and are
// looked up after the chroot, where that file is the step's.
func TestAUserSpecIsSplitIntoItsParts(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		spec        string
		user, group string
		numeric     bool
	}{
		{"testuser", "testuser", "", false},
		{"testuser:testgroup", "testuser", "testgroup", false},
		{"1000", "1000", "", true},
		{"1000:1000", "1000", "1000", true},
		// A numeric user with a named group is not numeric as a whole: the
		// group still needs the step's /etc/group.
		{"1000:staff", "1000", "staff", false},
		{"", "", "", false},
	} {
		t.Run(c.spec, func(t *testing.T) {
			t.Parallel()

			u, g, num := splitUserSpec(c.spec)
			if u != c.user || g != c.group || num != c.numeric {
				t.Errorf("splitUserSpec(%q) = (%q, %q, %v), want (%q, %q, %v)",
					c.spec, u, g, num, c.user, c.group, c.numeric)
			}
		})
	}
}
