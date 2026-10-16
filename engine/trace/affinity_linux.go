//go:build linux

package trace

import (
	"fmt"
	"runtime"

	"golang.org/x/sys/unix"
)

// Pin confines the calling thread to one CPU.
//
// **The caller must already hold its thread.** Affinity belongs to a thread,
// not to a goroutine, and a goroutine that is not locked may be moved onto
// another thread between one statement and the next - so an unlocked caller
// pins a thread it is about to stop using and leaves the pin behind for
// whatever runs there next.
//
// Why a tracer wants this: a traced path call costs 2.2µs when the stopped
// thread and the thread answering it share a CPU, and 45µs when they do not.
// Under a hypervisor each half of that wakeup is a vmexit - an idle vCPU has
// halted and has to be resumed by the VMM - which is why bare metal pays 8.3µs
// against 7.2µs for the same choice and the guest pays 19x (E681).
func Pin(cpu int) error {
	if cpu < 0 || cpu >= runtime.NumCPU() {
		return fmt.Errorf("pin to CPU %d: this machine has %d", cpu, runtime.NumCPU())
	}

	var set unix.CPUSet

	set.Zero()
	set.Set(cpu)

	// 0 is the calling thread, not the process: `sched_setaffinity(2)` takes a
	// thread id, and a zero one means "me". Passing `os.Getpid()` would move
	// the whole guest, tracer and every other request with it.
	err := unix.SchedSetaffinity(0, &set)
	if err != nil {
		return fmt.Errorf("pin this thread to CPU %d: %w", cpu, err)
	}

	return nil
}
