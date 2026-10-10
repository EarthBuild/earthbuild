package cli

import (
	"runtime"
	"strconv"
)

// EnvParallelism bounds how many steps a build runs at once.
//
// Unset is one per core, which is what the scheduler does with an unset field
// and what every build did before this existed.
//
// **A serial build is a diagnostic instrument.** `Scheduler.Parallelism` has
// always been there and nothing set it, so a build that stops with eight steps
// in flight could not be run one step at a time to find out whether the
// concurrency was the cause.
const EnvParallelism = "EARTH_PARALLELISM"

// parallelismFrom reads the limit, or zero for the default.
//
// A value that is not a positive number is the default rather than an error: it
// bounds how fast the build goes and nothing about what it produces, so a typo
// should not stop a build that would otherwise have run.
func parallelismFrom(look func(string) string) int {
	n, err := strconv.Atoi(look(EnvParallelism))
	if err != nil || n <= 0 {
		return 0
	}

	return n
}

// sandboxCPUs is a sandbox that runs steps somewhere with a processor count of
// its own. A sandbox on this machine does not implement it.
type sandboxCPUs interface {
	CPUs() int
}

// parallelismFor is how many steps this build may run at once.
//
// **The host's core count is the wrong number when the steps run elsewhere.** A
// microVM is given four vCPUs and two gigabytes; the machine that starts it may
// have thirty-two cores, and one-step-per-core then puts thirty-two concurrent
// steps inside a four-vCPU guest, each unpacking layers and running a package
// manager in shared memory. What that produces is not a clean failure: it is a
// step that exits non-zero having printed nothing, which reads as the command
// being wrong rather than as the machine being oversubscribed.
//
// What the invoker asked for wins over both, because the setting exists to make
// a build serial and a sandbox overriding that would take the instrument away.
//
// A sandbox is not believed past this machine's own count: its processors are
// this machine's, however many it claims, and oversubscribing them is the same
// mistake in the other direction.
func parallelismFor(sb any, look func(string) string) int {
	if n := parallelismFrom(look); n > 0 {
		return n
	}

	s, ok := sb.(sandboxCPUs)
	if !ok {
		// No opinion, so the scheduler's own default - one per core - is right.
		return 0
	}

	return min(s.CPUs(), runtime.NumCPU())
}
