//go:build linux

package guest

// cgroupPathOf is where a step's control group lives, or "" if it has none.
//
// The path is what `memory.events` is read from: the kernel counts an OOM kill
// there and nowhere else, and by the time a caller sees the failure the process
// that was killed is gone.
func cgroupPathOf(c *cgroup) string {
	if c == nil {
		return ""
	}

	return c.path
}
