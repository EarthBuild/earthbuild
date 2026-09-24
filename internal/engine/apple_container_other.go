//go:build !darwin

package engine

func defaultContainerMemory() string {
	return containerMemoryFor(0)
}

// IsMemoryPressured returns true if the host is experiencing elevated memory pressure.
func IsMemoryPressured() bool {
	return false
}
