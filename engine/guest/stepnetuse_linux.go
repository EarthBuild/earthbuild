//go:build linux

package guest

import (
	"bytes"
	"fmt"
	"os"
	osexec "os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

// netnsDir is where `ip netns` keeps the bind mounts that hold namespaces open.
const netnsDir = "/var/run/netns/"

// privateStepNet reports whether steps get a network namespace of their own.
//
// Read once. A build that answered differently for two steps would be a build
// where some steps could reach each other's ports and some could not, which is
// worse than either answer.
var privateStepNet = sync.OnceValue(func() bool {
	return os.Getenv(EnvStepNet) == NetPrivate
})

// nextStepNet numbers the namespaces, so two live steps never share addresses.
//
// Monotonic rather than a free list. A returned number could be handed out
// again while the kernel was still tearing down the veth that used it, and the
// second setup would fail on a name the first had not finished releasing.
var nextStepNet atomic.Int64

// openStepNet builds a network namespace for one step, or says why it did not.
//
// Three returns rather than an error, because the third case is neither success
// nor failure: no `ip` on the guest means the step runs shared, which is what it
// did yesterday and every day before. I11 - degrade, and say so.
func openStepNet() (path string, done func(), why string) {
	nothing := func() {}

	if !privateStepNet() {
		return "", nothing, ""
	}

	for _, prog := range []string{"ip", "iptables"} {
		_, err := osexec.LookPath(prog)
		if err != nil {
			return "", nothing, prog + " is not on the guest's PATH, so a step" +
				" cannot be given a network of its own"
		}
	}

	plan := stepNetPlan(int(nextStepNet.Add(1)))

	for _, argv := range stepNetUp(plan) {
		out, err := osexec.Command(argv[0], argv[1:]...).CombinedOutput() //nolint:gosec // a fixed vocabulary, built from a plan
		if err != nil {
			// Half a namespace is worse than none: it would take the step's
			// address without giving it a route, so the step would come up
			// isolated and fail on its first fetch rather than here.
			closeStepNet(plan)

			return "", nothing, fmt.Sprintf("%s: %v: %s",
				strings.Join(argv, " "), err, bytes.TrimSpace(out))
		}
	}

	return netnsDir + plan.Name, func() { closeStepNet(plan) }, ""
}

// closeStepNet undoes what it can and reports nothing.
//
// Best-effort by design: it runs on the way out of a step that may already have
// failed, and a teardown error reported there would displace the reason the step
// failed with a reason nobody can act on. What it must not do is stop early - a
// namespace left behind holds an address that `nextStepNet` will not reissue,
// but a NAT rule left behind accumulates in the guest's tables.
func closeStepNet(plan netPlan) {
	for _, argv := range stepNetDown(plan) {
		_ = osexec.Command(argv[0], argv[1:]...).Run() //nolint:gosec // a fixed vocabulary, built from a plan
	}
}
