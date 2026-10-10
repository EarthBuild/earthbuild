//go:build linux

package exec

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

// How large a guest is when nobody says.
//
// **The guest is the build machine, not a helper beside it.** It unpacks the
// layers, runs the steps and does the compiling, while the process that started
// it mostly waits - so a small slice of the host is exactly the wrong shape.
// Four vCPUs and two gigabytes is a serverless-function default, and a build is
// not a function.
const (
	// minMemoryMiB is what a guest gets when half of this machine is less than
	// a guest needs, and when the machine cannot be asked. A guest given less
	// than this unpacks a large image into a tmpfs and stops.
	minMemoryMiB = 2048

	// memInfo is where Linux states the machine's memory. A file rather than a
	// syscall because `sysinfo` reports what is free as well, and what is free
	// now says nothing about what this build may have.
	memInfo = "/proc/meminfo"
)

// defaultCPUs is every processor this machine has.
//
// All of them, because the parallelism follows this number and the host has
// nothing else to do while the guest builds: the engine is waiting on it.
// `parallelismFor` still refuses to believe a guest past this machine's own
// count, so the two agree by construction.
func defaultCPUs() int { return runtime.NumCPU() }

// defaultMemory is half this machine's memory.
//
// **Half, because a VM's memory is committed**: the host cannot use what the
// guest has been given, so taking all of it is how a build takes the machine
// down with it. Half is the split Podman and Docker Desktop use and the one
// anybody debugging this will expect.
func defaultMemory() int {
	b, err := os.ReadFile(memInfo)
	if err != nil {
		return minMemoryMiB
	}

	return memoryFrom(string(b))
}

// memoryFrom reads MemTotal and halves it, in MiB.
//
// Anything it cannot read is the floor rather than zero: a guest given no
// memory does not boot, and this is a default rather than an answer somebody
// asked for.
func memoryFrom(meminfo string) int {
	for line := range strings.SplitSeq(meminfo, "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			break
		}

		kb, err := strconv.Atoi(fields[1])
		if err != nil {
			break
		}

		return max(kb/1024/2, minMemoryMiB)
	}

	return minMemoryMiB
}
