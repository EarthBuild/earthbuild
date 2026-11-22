//go:build linux

package guest

import (
	"os"
	"syscall"
	"time"
)

// usageOf reads what a finished process spent.
//
// `--exec-stats` asks a build to say how much CPU and memory it used, and the
// only place that can answer is the one holding the process: the kernel reports
// it to the parent at wait, and by the time a result reaches the host the
// process is gone (E467).
//
// Zero for a state that carries no usage, which is a state from a process that
// never started - and a build reporting nothing is a build that says nothing,
// where reporting a made-up number is worse.
func usageOf(st *os.ProcessState) (cpu time.Duration, maxRSS uint64) {
	if st == nil {
		return 0, 0
	}

	ru, ok := st.SysUsage().(*syscall.Rusage)
	if !ok {
		return 0, 0
	}

	// User and system together, because a step waiting on the kernel is a step
	// spending the machine's time. `ProcessState` has both separately and every
	// summary anyone writes adds them.
	cpu = st.UserTime() + st.SystemTime()

	// Kilobytes on Linux, bytes on darwin - a difference the man page states
	// and every reader of `ru_maxrss` gets wrong once. This file is Linux.
	return cpu, uint64(ru.Maxrss) * 1024 //nolint:gosec // a kernel counter
}
