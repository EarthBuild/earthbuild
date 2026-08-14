//go:build darwin

package containerutil

import (
	"os"
	"runtime"
	"strconv"

	"golang.org/x/sys/unix"
)

// defaultContainerResources dynamically detects host resources on darwin and allocates
// 75% of host memory and all CPU cores by default for Apple Container VMs.
func defaultContainerResources() (cpus int, memoryMB int) {
	cpus = runtime.NumCPU()

	if memBytes, err := unix.SysctlUint64("hw.memsize"); err == nil && memBytes > 0 {
		// Allocate 75% of total system RAM by default.
		memoryMB = int((memBytes * 3 / 4) / (1024 * 1024))
	}
	if memoryMB < 4096 {
		memoryMB = 4096
	}

	if env := os.Getenv("EARTH_CONTAINER_CPUS"); env != "" {
		if v, err := strconv.Atoi(env); err == nil && v > 0 {
			cpus = v
		}
	}

	if env := os.Getenv("EARTH_CONTAINER_MEMORY_MB"); env != "" {
		if v, err := strconv.Atoi(env); err == nil && v > 0 {
			memoryMB = v
		}
	}

	return cpus, memoryMB
}
