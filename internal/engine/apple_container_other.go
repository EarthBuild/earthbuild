//go:build !darwin

package engine

import "runtime"

func defaultContainerResources() (cpus int, memoryMB int) {
	return runtime.NumCPU(), 4096
}

// IsMemoryPressured returns true if the host is experiencing elevated memory pressure.
func IsMemoryPressured() bool {
	return false
}
