//go:build linux

package exec

import (
	"os"
	osexec "os/exec"
	"os/user"
	"strconv"
	"strings"
)

// hostRootlessProbe asks this machine the three questions a rootless daemon's
// prospects turn on.
//
// Each answer is a file or a PATH lookup and none of them starts anything: the
// point is to be able to say what is missing while refusing, not to try.
//
// **Nothing calls this yet, and that is the honest state rather than an
// oversight.** It feeds `couldHost`, which is reached through `sharedDockerFor`
// - and on Linux E380 gave a step a daemon of its own, so the refusal this was
// written to improve is no longer on the path. Rootless is a deferred item
// (I10), the probe is what will make its refusal say something useful when it
// arrives, and `couldHost` already has the branch that admits the check has not
// been run. Deleting it would throw away the answer and keep the question.
func hostRootlessProbe() rootlessProbe { //nolint:unused // the deferred-rootless diagnostic; see the note above
	return rootlessProbe{
		look: func(prog string) (string, bool) {
			p, err := osexec.LookPath(prog)

			return p, err == nil
		},
		subid:  userHasSubIDs,
		userns: maxUserNamespaces,
	}
}

// userHasSubIDs is whether this user has a range allocated in /etc/subuid or
// /etc/subgid.
//
// Matched on name **and** numeric id, because both spellings appear in the wild
// and a check that knew one would tell a correctly-configured operator their
// machine is not configured.
func userHasSubIDs(file string) (bool, error) { //nolint:unused // reached only from hostRootlessProbe
	b, err := os.ReadFile(file) //nolint:gosec // a path this engine names
	if err != nil {
		return false, err //nolint:wrapcheck // the caller says what it was asking
	}

	me, err := user.Current()
	if err != nil {
		return false, err //nolint:wrapcheck // the caller says what it was asking
	}

	for line := range strings.SplitSeq(string(b), "\n") {
		who, _, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && (who == me.Username || who == me.Uid) {
			return true, nil
		}
	}

	return false, nil
}

// maxUserNamespaces is how many the kernel will allow this process to create.
//
// Zero is how a distribution switches the feature off, and is the one of the
// three an operator most often cannot change - so it is read rather than
// assumed. A kernel too old to have the knob allows them, which is why a missing
// file is not zero.
func maxUserNamespaces() (int, error) { //nolint:unused // reached only from hostRootlessProbe
	b, err := os.ReadFile("/proc/sys/user/max_user_namespaces")
	if os.IsNotExist(err) {
		return 1, nil
	}

	if err != nil {
		return 0, err //nolint:wrapcheck // the caller says what it was asking
	}

	return strconv.Atoi(strings.TrimSpace(string(b)))
}
