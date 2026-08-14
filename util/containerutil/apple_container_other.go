//go:build !darwin

package containerutil

import "runtime"

func defaultContainerResources() (cpus int, memoryMB int) {
	return runtime.NumCPU(), 4096
}
