package exec

import "fmt"

// outerDaemonUsable decides whether a build may use the daemon whose socket it
// can already see, and says why not when it may not.
//
// The three inputs are three separate facts and the decision needs all of them:
//
//   - **inside**: this build is running in a container. That is what makes an
//     inherited socket the *outer step's* daemon rather than a machine's, and it
//     is the only case that needs nobody's permission - the outer step already
//     decided what that daemon shares, and this build is inside its blast
//     radius by construction.
//   - **socket**: there is something to inherit at all.
//   - **allowed**: the operator said yes to this machine's own daemon
//     (EARTH_ALLOW_HOST_DOCKER), which is the existing permission and means the
//     same thing here as it does everywhere else.
//
// The dangerous case is a socket without a container. The daemon on the end of
// it is the machine's, which is root on the machine (E145): every image the
// build touches outlives it, and a step can write to any of them. Refused unless
// the operator has said otherwise.
//
// **Independent of which way the default falls.** Whether an author opts in to
// sharing or opts out of it, both spellings have to answer this same question,
// and it answers the same way.
func outerDaemonUsable(inside, socket, allowed bool) (bool, string) {
	if !socket {
		return false, "there is no daemon to share: nothing is listening where an" +
			" outer step would have left a socket, and a build that inherited" +
			" nothing would fail later as though a daemon had broken"
	}

	if inside || allowed {
		return true, ""
	}

	return false, fmt.Sprintf(
		"the daemon on that socket belongs to this machine rather than to an outer"+
			"\n  step, and it is root on it: every image this build touches would"+
			"\n  outlive the build, and any step could write to any of them"+
			"\n  set %s=1 to say that is what you meant", envAllowHostDocker)
}
