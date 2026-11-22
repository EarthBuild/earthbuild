package exec

import (
	"os"
	"path/filepath"
	"runtime"
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

	// **On the first line, because that is the only line anything keeps.** The
	// corpus gate records `firstLine(err.Error())`, so a holder named on the
	// second line is a holder nobody reads - which is how 36 refusals were
	// diagnosed twice from a message that had the answer in it all along.
	if got := describeHolders(nil); !strings.Contains(got, "another build") {
		t.Errorf("an unknown holder reads as %q, which does not say a build"+
			" holds it", got)
	}

	if strings.Contains(describeHolders([]int{me}), "\n") {
		t.Error("the holder is on its own line, where firstLine drops it")
	}
}

// The whole path, against a real lock: a claim refused by this very process
// says so.
//
// The parser and the sentence are covered above with fixtures; this covers the
// part fixtures cannot - that the inode a caller stats is the inode the kernel
// prints, on this machine's filesystem. It was wrong about exactly that once.
func TestARefusedClaimNamesThisProcessInPractice(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/proc/locks is a Linux facility")
	}

	at := filepath.Join(t.TempDir(), "store.img")

	err := os.WriteFile(at, make([]byte, 4096), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	held, err := claimStore(at)
	if err != nil {
		t.Fatalf("the first claim was refused: %v", err)
	}

	defer held()

	_, err = claimStore(at)
	if err == nil {
		t.Fatal("a second claim on a held device succeeded")
	}

	if !strings.Contains(err.Error(), "this build itself") {
		t.Errorf("the refusal does not name the holder, so it cannot say"+
			" whether this is another terminal or a sandbox this build left"+
			" running:\n%v", err)
	}
}
