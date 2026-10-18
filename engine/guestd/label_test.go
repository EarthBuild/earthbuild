package guestd

import (
	"os"
	"testing"
)

// A diagnostic names the command the operator actually typed.
//
// The agent is reachable two ways - as `earth-guestd`, and as `earth guestd`
// out of the CLI itself - and a message that always says `earth-guestd` sends
// somebody looking for a binary that a one-file installation does not have. The
// rule this repo follows is that an error says where it happened; the name of a
// file that is not there is the opposite of that.
//
//nolint:paralleltest // writes os.Args, which every other test reads
func TestADiagnosticNamesHowItWasInvoked(t *testing.T) {
	for _, c := range []struct {
		name string
		argv []string
		want string
	}{
		{"standalone", []string{"/usr/local/bin/earth-guestd", "--fills"}, "earth-guestd"},
		{"subcommand", []string{"/usr/local/bin/earth", "guestd", "--fills"}, "earth guestd"},
		{"subcommand under another name", []string{"./earthly", "guestd"}, "earthly guestd"},
	} {
		t.Run(c.name, func(t *testing.T) { //nolint:paralleltest // as above
			old := os.Args
			os.Args = c.argv

			t.Cleanup(func() { os.Args = old })

			if got := label(); got != c.want {
				t.Errorf("label() = %q, want %q", got, c.want)
			}
		})
	}
}

// The error itself does not repeat the program's name.
//
// `earth-guestd: earth-guestd requires Linux` is what happens when both the
// printer and the error carry it. The printer owns the prefix.
func TestAnErrorDoesNotCarryTheProgramName(t *testing.T) {
	t.Parallel()

	_, _, err := newMaterialiser(t.TempDir(), t.TempDir())
	if err == nil {
		t.Skip("this platform has a materialiser, so there is no refusal to read")
	}

	for _, bad := range []string{"earth-guestd", "earth guestd"} {
		if got := err.Error(); len(got) >= len(bad) && got[:len(bad)] == bad {
			t.Errorf("the error begins %q; the printer adds that:\n  %s", bad, got)
		}
	}
}
