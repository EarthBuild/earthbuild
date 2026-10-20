//go:build !darwin

package engine

func defaultContainerMemory() string {
	return "4096M"
}

// IsMemoryPressured returns true if the host is experiencing elevated memory pressure.
func IsMemoryPressured() bool {
	return false
}
