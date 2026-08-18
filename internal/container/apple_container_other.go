//go:build !darwin

package container

import "runtime"

func defaultContainerResources() (cpus int, memoryMB int) {
	return runtime.NumCPU(), 4096
}
