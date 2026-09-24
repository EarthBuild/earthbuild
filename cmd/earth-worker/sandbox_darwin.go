//go:build darwin

package main

import (
	"fmt"
	"os"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// workerSandbox is where this worker runs steps.
//
// The Apple backend: a VM with the guest inside it, which is the same one a
// local build uses on this platform. **A delegate is an engine** (C.3), and the
// way to be sure of that is to be the same code - which is the reason the Linux
// backend reaches for `exec.NewNative()` rather than arranging something of its
// own.
//
// What a darwin worker offers is ordinary `linux/arm64` work. Running `LOCALLY`
// steps *as macOS* is a different thing wearing the same word: it needs the
// platform to reach placement as something other than `linux/*`, and it is a
// plan item rather than this (E501).
//
// A store of its own is required rather than defaulted. Two workers on one
// machine must not share a layer store - and on darwin that matters twice over,
// because the VM is named after its mounts, so the store is also what keeps
// their machines apart.
func workerSandbox() (exec.Sandbox, error) {
	root := os.Getenv("EARTH_CACHE_DIR")
	if root == "" {
		return nil, fmt.Errorf("set EARTH_CACHE_DIR to where this worker keeps" +
			" its layers" +
			"\n  a worker materialises bases and captures results, so it needs" +
			" a store of its own")
	}

	sb := exec.NewApple()
	sb.Store = root

	// Asked before the worker joins anything. A worker that joined a fleet and
	// then refused every step would be worse than one that did not join: the
	// driver would keep sending it work and keep getting it back.
	err := sb.Available()
	if err != nil {
		return nil, fmt.Errorf("this machine cannot run steps: %w", err)
	}

	return sb, nil
}
