//go:build darwin

package engine

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
		// Allocate 25% of total system RAM by default.
		//nolint:gosec // memory size in MB will not realistically overflow int
		memoryMB = int((memBytes / 4) / (1024 * 1024))
	}

	memoryMB = max(memoryMB, 4096)

	return cpus, memoryMB
}

// IsMemoryPressured returns true if the host is experiencing elevated memory pressure (Warning or Critical).
func IsMemoryPressured() bool {
	// Check macOS memory pressure level (1 = normal, 2 = warning, 4 = critical)
	level, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level")
	return err == nil && level >= 2
}
