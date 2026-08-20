//go:build linux

package main

import (
	"fmt"
	"os"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// workerSandbox is where this worker runs steps.
//
// The native backend: the guest as a child process, confined with namespaces
// and cgroups, which is the same one a local build uses on this platform.
func workerSandbox() (exec.Sandbox, error) {
	root := os.Getenv("EARTH_CACHE_DIR")
	if root == "" {
		return nil, fmt.Errorf("set EARTH_CACHE_DIR to where this worker keeps" +
			" its layers" +
			"\n  a worker materialises bases and captures results, so it needs" +
			" a store of its own")
	}

	sb := exec.NewNative()
	sb.Root = root

	err := sb.Available()
	if err != nil {
		return nil, fmt.Errorf("this machine cannot run steps: %w", err)
	}

	return sb, nil
}
