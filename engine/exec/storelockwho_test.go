package exec

import (
	"os"
	"strings"
	"testing"
)

// The claim names who holds it, because "in use by another build" is the one
// thing the reader already knew.
//
// **A refusal that cannot be acted on is a refusal that gets worked around.**
// The corpus gate met this 42 times in one run and the message could not say
// whether the holder was a build running in another terminal, a VMM left behind
// by a build that had ended, or the very process being refused - and those have
// three different answers. The kernel knows: /proc/locks carries the pid of
// every flock holder against the inode it is held on.
func TestTheStoreClaimNamesWhoHoldsIt(t *testing.T) {
	t.Parallel()

	// The shape the kernel writes: id, kind, mode, access, pid, MAJ:MIN:INO,
	// start, end.
	locks := strings.Join([]string{
		"1: POSIX  ADVISORY  WRITE 811 08:02:1179651 0 EOF",
		"2: FLOCK  ADVISORY  WRITE 4242 08:02:9876543 0 EOF",
		"3: FLOCK  ADVISORY  READ  99 08:02:1111111 0 EOF",
	}, "\n")

	got := flockHolders(locks, 9876543)
	if len(got) != 1 || got[0] != 4242 {
		t.Errorf("holders of inode 9876543 read as %v, want [4242]", got)
	}

	if h := flockHolders(locks, 1179651); len(h) != 0 {
		t.Errorf("a POSIX record was read as an flock holder: %v", h)
	}

	if h := flockHolders(locks, 404); len(h) != 0 {
		t.Errorf("an inode nothing holds read as held by %v", h)
	}
}

// Being refused by oneself is a different fault and says so.
func TestAClaimHeldByThisProcessSaysSo(t *testing.T) {
	t.Parallel()

	me := os.Getpid()

	if got := describeHolders([]int{me}); !strings.Contains(got, "this build") {
		t.Errorf("a lock held by this very process reads as %q,"+
			" which sends the reader looking for another terminal", got)
	}

	if got := describeHolders([]int{me + 1}); strings.Contains(got, "this build") {
		t.Errorf("a lock held elsewhere reads as %q", got)
	}

	if got := describeHolders(nil); got != "" {
		t.Errorf("no holders should add nothing, got %q", got)
	}
}
