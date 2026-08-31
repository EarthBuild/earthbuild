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
	return os.Getenv(EnvStepNet) != NetShared
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

	// **No reachable resolver, no private namespace.** A step in its own
	// namespace cannot use a loopback nameserver, and every name it looks up is
	// a name it fails to find - which is E931, and which cost twelve CI jobs.
	// Sharing resolves; isolating without a resolver does not, so the honest
	// answer is to share and say why.
	//
	// Not a public fallback. Inventing 8.8.8.8 here would send a build's lookups
	// to a third party nobody named, which is not this engine's decision to
	// make quietly.
	if len(hostNameservers()) == 0 {
		return "", nothing, "this machine resolves through a loopback address" +
			" only, which a step in its own network namespace cannot reach"
	}

	// **Retried, because the counter is per process and the register is not.**
	// A build runs many `earth` processes - the outer one, and a nested `earth`
	// inside every step that starts one - each numbering from zero into a
	// `/run/netns` they share. They all asked for `earth-s1`, the second got
	// `Cannot create namespace file: File exists`, degraded to shared, and
	// printed a warning into output that tests compare: five Native jobs broken
	// to fix one (E933).
	//
	// Retrying rather than salting the name. A salt has to be unique among live
	// processes *and* fit the addresses - 10.201.0.0/16 holds 16384 blocks and a
	// pid does not fit beside a counter in that - so a constructed name is a
	// second thing to get right. Taking the next free one is correct however the
	// collision arose, including against a namespace some earlier build left
	// behind.
	var last string

	for range stepNetTries {
		plan := stepNetPlan(int(nextStepNet.Add(1)))

		last = raiseStepNet(plan)
		if last == "" {
			return netnsDir + plan.Name, func() { closeStepNet(plan) }, ""
		}
	}

	return "", nothing, last
}

// stepNetTries bounds the search for a free name.
//
// Small, because a machine with sixteen consecutive namespaces taken has
// something wrong with it that another attempt will not fix - and each try costs
// an `ip` invocation whether it succeeds or not.
const stepNetTries = 16

// raiseStepNet builds one namespace, or says why it could not.
func raiseStepNet(plan netPlan) string {
	for _, argv := range stepNetUp(plan) {
		out, err := osexec.Command(argv[0], argv[1:]...).CombinedOutput() //nolint:gosec // a fixed vocabulary, built from a plan
		if err != nil {
			// Half a namespace is worse than none: it would take the step's
			// address without giving it a route, so the step would come up
			// isolated and fail on its first fetch rather than here.
			closeStepNet(plan)

			return fmt.Sprintf("%s: %v: %s",
				strings.Join(argv, " "), err, bytes.TrimSpace(out))
		}
	}

	return ""
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
