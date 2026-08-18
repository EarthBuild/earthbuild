//go:build darwin

package containerutil

import (
	"runtime"

	"golang.org/x/sys/unix"
)

// defaultContainerResources dynamically detects host resources on darwin and allocates
// 75% of host memory and all CPU cores by default for Apple Container VMs.
func defaultContainerResources() (cpus, memoryMB int) {
	cpus = runtime.NumCPU()

	memBytes, err := unix.SysctlUint64("hw.memsize")
	if err == nil && memBytes > 0 {
		// Allocate 75% of total system RAM by default.
		//nolint:gosec // memory size in MB will not realistically overflow int
		memoryMB = int((memBytes * 3 / 4) / (1024 * 1024))
	}

	memoryMB = max(memoryMB, 4096)

	return cpus, memoryMB
}
