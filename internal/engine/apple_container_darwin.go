//go:build darwin

package engine

import (
	"fmt"

	"golang.org/x/sys/unix"
)

const mb = 1024 * 1024

// defaultContainerMemory dynamically detects host memory on darwin and allocates
// 25% of host memory by default for Apple Container VMs.
func defaultContainerMemory() string {
	var mem uint64

	memsize, err := unix.SysctlUint64("hw.memsize")
	if err == nil && memsize > 0 {
		mem = (memsize / 4) / mb
	}

	return fmt.Sprintf("%dM", max(4096, mem))
}

// IsMemoryPressured returns true if the host is experiencing elevated memory pressure (Warning or Critical).
func IsMemoryPressured() bool {
	// Check macOS memory pressure level (1 = normal, 2 = warning, 4 = critical)
	level, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level")
	return err == nil && level >= 2
}
