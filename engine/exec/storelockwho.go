package exec

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// whoHolds describes the processes holding an flock on a path, or "" when it
// cannot tell.
//
// **Because "in use by another build" is the one thing the reader knew
// already.** The refusal has three different causes with three different
// answers - a build running in another terminal, a VMM left behind by a build
// that has ended, or this very process refusing itself - and the message could
// not tell them apart. The kernel can: /proc/locks carries the pid of every
// flock holder against the inode it is held on.
//
// Best effort throughout. This runs on a path that is already failing, and a
// diagnostic that can itself fail is one more thing to diagnose: everything
// here degrades to the empty string, which leaves the original message intact.
func whoHolds(at string) string {
	ino, ok := storeInode(at)
	if !ok {
		return ""
	}

	// Absent on anything that is not Linux, which is the degrade rather than a
	// build tag: the store device is a Linux facility and the message is worth
	// having on the platform that has one.
	b, err := os.ReadFile("/proc/locks")
	if err != nil {
		return ""
	}

	return describeHolders(flockHolders(string(b), ino))
}

// flockHolders returns the pids holding an flock on an inode.
//
// The kernel writes one record per line, and only some of them are flocks:
//
//	2: FLOCK  ADVISORY  WRITE 4242 08:02:9876543 0 EOF
//	   ^kind                 ^pid  ^maj:min:inode
//
// Matched on the inode alone rather than on device too, because the device
// numbers /proc/locks prints are the *containing* filesystem's and a caller
// holding a path has no cheap way to agree with the kernel about what those
// are. An inode collision would name an unrelated pid; naming the wrong pid in
// a diagnostic is a smaller fault than naming none.
func flockHolders(locks string, ino uint64) []int {
	var found []int

	for _, line := range strings.Split(locks, "\n") {
		f := strings.Fields(line)
		if len(f) < 6 || f[1] != "FLOCK" {
			continue
		}

		pid, err := strconv.Atoi(f[4])
		if err != nil {
			continue
		}

		where := strings.Split(f[5], ":")
		if len(where) != 3 {
			continue
		}

		got, err := strconv.ParseUint(where[2], 10, 64)
		if err != nil || got != ino {
			continue
		}

		found = append(found, pid)
	}

	return found
}

// describeHolders turns pids into the phrase that follows "in use".
//
// **One line, and the first one.** Every caller that records this records
// `firstLine(err.Error())`, so a holder named underneath is a holder nobody
// reads: 36 refusals in one corpus run were diagnosed twice over from a message
// that had been carrying the answer on line two.
func describeHolders(pids []int) string {
	if len(pids) == 0 {
		// Not "by nobody": flock said the lock was held, so it was. What this
		// means is that the holder had gone by the time the question was asked,
		// which is itself the diagnosis - a guest on its way out rather than a
		// build that is running.
		return " by another build, which had already released it when asked"
	}

	var out []string

	for _, pid := range pids {
		switch {
		case pid == os.Getpid():
			out = append(out, fmt.Sprintf("this build itself (pid %d):"+
				" a sandbox it started has not been stopped", pid))
		default:
			out = append(out, fmt.Sprintf("build %d (%s)", pid, commOf(pid)))
		}
	}

	return " by " + strings.Join(out, ", ")
}

// commOf names a process, or says it has gone.
func commOf(pid int) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return "no longer running"
	}

	return strings.TrimSpace(string(b))
}
