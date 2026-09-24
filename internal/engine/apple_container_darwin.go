//go:build darwin

package engine

import (
	"golang.org/x/sys/unix"
)

// defaultContainerMemory is this host's ceiling for a BuildKit VM. See
// containerMemoryFor for the rule and why.
func defaultContainerMemory() string {
	memsize, _ := unix.SysctlUint64("hw.memsize")

	return containerMemoryFor(memsize)
}

// IsMemoryPressured returns true if the host is experiencing elevated memory pressure (Warning or Critical).
func IsMemoryPressured() bool {
	// Check macOS memory pressure level (1 = normal, 2 = warning, 4 = critical)
	level, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level")
	return err == nil && level >= 2
}
