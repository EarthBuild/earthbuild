//go:build !darwin

package engine

import "runtime"

func defaultContainerResources() (cpus int, memoryMB int) {
	return runtime.NumCPU(), 4096
}
