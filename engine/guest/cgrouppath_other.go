//go:build !linux

package guest

// cgroupPathOf has no control group to point at off Linux.
func cgroupPathOf(*cgroup) string { return "" }
