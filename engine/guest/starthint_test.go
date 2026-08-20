package guest

import (
	"errors"
	"strings"
	"syscall"
	"testing"
)

// A step that could not be started says which of the reasons it was.
//
// `fork/exec /bin/sh: operation not permitted` names the binary, which is the
// one thing that is not the problem: EPERM means the kernel refused the call,
// not that the file is missing or unreadable. It has been seen twice in full
// gate runs, in different targets, and every hypothesis about it so far has
// been reasoning rather than evidence - because the message carries none.
//
// The distinction that matters is between the *binary* and the *isolation*.
// Starting a confined step chroots and unshares four namespaces, and each of
// those returns EPERM without the capability for it; exec of a missing file
// returns ENOENT and of a non-executable one EACCES. One message cannot be all
// three, so the facts are gathered and stated.
func TestAStepThatCouldNotStartSaysWhy(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		err   error
		facts startFacts
		want  []string
	}{
		{
			// The sighting. Root, the binary is there and executable, and the
			// kernel still said no - so it is the isolation that was refused,
			// and the capabilities are the thing to look at.
			name: "permission denied with everything in place",
			err:  syscall.EPERM,
			facts: startFacts{
				Euid: 0, Confined: true,
				Binary: testShell, BinaryMode: "-rwxr-xr-x", Caps: "CapEff: 0000000000000000",
			},
			want: []string{
				"the binary is there and executable",
				"chroot",
				"CapEff",
			},
		},
		{
			// Unconfined: no chroot, no namespaces, so the isolation cannot be
			// what was refused and saying so would send the reader the wrong way.
			name: "permission denied with no isolation applied",
			err:  syscall.EPERM,
			facts: startFacts{
				Euid: 0, Confined: false,
				Binary: testShell, BinaryMode: "-rwxr-xr-x",
			},
			want: []string{"no isolation was applied"},
		},
		{
			name: "the binary is not in the image",
			err:  syscall.ENOENT,
			facts: startFacts{
				Euid: 0, Confined: true, Binary: "", BinaryMode: binaryMissing,
			},
			want: []string{binaryMissing},
		},
		{
			name: "the binary is not executable",
			err:  syscall.EACCES,
			facts: startFacts{
				Euid: 1000, Confined: true,
				Binary: testShell, BinaryMode: "-rw-r--r--",
			},
			want: []string{"-rw-r--r--", "euid 1000"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := startHint(tc.err, tc.facts)

			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("the hint does not mention %q:\n%s", want, got)
				}
			}
		})
	}
}

// An ordinary failure gets no hint.
//
// A step that failed for a reason the message already carries does not need
// three lines of filesystem trivia under it, and a hint that always appears is
// a hint nobody reads.
func TestAnOrdinaryStartFailureGetsNoHint(t *testing.T) {
	t.Parallel()

	if got := startHint(errors.New("context canceled"), startFacts{}); got != "" {
		t.Errorf("a hint was invented for an unrelated failure:\n%s", got)
	}
}
