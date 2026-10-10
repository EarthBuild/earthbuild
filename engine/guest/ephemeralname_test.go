//go:build linux

package guest

import "testing"

// The id of a shared ephemeral mount becomes a directory inside the sandbox, and
// it arrives in a step assignment from a peer this guest did not write (A5). A
// name that needed cleaning was never a name, so it is refused rather than
// repaired.
func TestAnEphemeralNameIsANameAndNotAPath(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		id string
		ok bool
	}{
		{"docker-scope/b7", true},
		{"b7", true},
		{"a_b-c.1", true},
		{"", false},
		{"../../etc", false},
		{"docker-scope/../../etc", false},
		{"/etc/passwd", false},
		{"docker-scope/", false},
		{"a/b/c", false},
		{"has space", false},
		{"semi;colon", false},
		{"..", false},
	} {
		if got := plainName(c.id); got != c.ok {
			t.Errorf("plainName(%q) = %v, want %v", c.id, got, c.ok)
		}
	}
}
