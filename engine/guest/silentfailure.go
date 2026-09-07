package guest

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// failure is what is known about a step that ran and did not succeed.
//
// Gathered at the moment it fails, where the process state, the clock and the
// cgroup are all still in hand. A minute later they are not, which is why this
// is assembled there rather than reconstructed by a reader.
type failure struct {
	exit     int
	signal   syscall.Signal
	cpu      time.Duration
	rss      uint64
	ran      time.Duration
	oomKills int
}

// noteFor is what to add to a failing step's output, and nothing when the step
// already said enough.
//
// **A step that printed something has said more than this could.** The note is
// for the case that defeats a reader: a process that exits non-zero and says
// nothing at all, where the exit code is the entire evidence.
func noteFor(out []byte, f failure) string {
	if len(out) > 0 {
		return ""
	}

	return silentNote(f)
}

// silentNote describes a failure that left no output.
//
// **Everything here was already in hand and was thrown away.** "exited 2, and
// printed nothing" cost an afternoon: the command was reproduced in isolation,
// run sixteen ways in parallel, and checked for lost output and crossed streams
// - all to learn things this line could have said at the time.
//
// Ordered by what decides the next move: whether the kernel killed it, then
// whether it ran at all, then what it consumed.
func silentNote(f failure) string {
	var said []string

	// **The one cause a reader cannot infer.** A process killed for memory
	// exits like any other and leaves nothing behind; the cgroup counted it.
	if f.oomKills > 0 {
		said = append(said, fmt.Sprintf("the kernel killed it for running out of"+
			" memory (%d time(s) in its cgroup)", f.oomKills))
	}

	// Go reports a signalled process as exit -1, so the number alone says "this
	// did not exit" and nothing about why.
	if f.signal != 0 {
		said = append(said, "killed by "+signalName(f.signal))
	}

	if f.ran > 0 {
		said = append(said, "ran for "+f.ran.Round(time.Millisecond).String())
	}

	if f.cpu > 0 {
		said = append(said, "used "+f.cpu.Round(time.Millisecond).String()+" of CPU")
	}

	if f.rss > 0 {
		said = append(said, fmt.Sprintf("peaked at %d MiB", f.rss>>20))
	}

	if len(said) == 0 {
		return "\n  it produced no output and left nothing else to go on"
	}

	return "\n  it printed nothing; " + strings.Join(said, ", ")
}

// oomKillsIn is how many times the kernel killed something in this cgroup for
// memory, or zero where it cannot be asked.
//
// `memory.events` is cgroup v2's own count, written by the kernel at the moment
// of the kill. Nothing else records it: the process is gone, its output is
// whatever it had flushed, and its exit status is indistinguishable from an
// ordinary one.
func oomKillsIn(dir string) int {
	if dir == "" {
		return 0
	}

	b, err := os.ReadFile(filepath.Join(dir, "memory.events")) //nolint:gosec // a cgroup path this package made
	if err != nil {
		return 0
	}

	for line := range strings.SplitSeq(string(b), "\n") {
		name, count, ok := strings.Cut(line, " ")
		if !ok || name != "oom_kill" {
			continue
		}

		n, err := strconv.Atoi(strings.TrimSpace(count))
		if err != nil {
			return 0
		}

		return n
	}

	return 0
}
