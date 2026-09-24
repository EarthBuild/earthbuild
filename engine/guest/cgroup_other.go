//go:build !linux

package guest

import "syscall"

// Limits bound what a step may consume. Off Linux there are no cgroups, so
// these are accepted and not applied - a degradation, never a refusal, because
// resource bounds are not a correctness property (see cgroup_linux.go).
type Limits struct {
	MemoryMax         int64
	PidsMax           int64
	CPUMax, CPUPeriod int64
}

// Empty reports whether any limit is set.
func (l Limits) Empty() bool {
	return l.MemoryMax == 0 && l.PidsMax == 0 && l.CPUMax == 0
}

type cgroup struct{}

func newCgroup(_ string, l Limits) (*cgroup, error) {
	if l.Empty() {
		return nil, nil //nolint:nilnil // nothing was asked for, so nothing was denied
	}

	// Reported rather than ignored: a caller that asked for a memory ceiling and
	// silently did not get one cannot tell a bounded build from an unbounded one.
	return degraded("this platform has no cgroups")
}

func (c *cgroup) apply(*syscall.SysProcAttr) {}
func (c *cgroup) remove() error              { return nil }
