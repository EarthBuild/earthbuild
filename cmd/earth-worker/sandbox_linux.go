//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/EarthBuild/earthbuild/engine/cli"
	"github.com/EarthBuild/earthbuild/engine/exec"
)

// workerSandbox is where this worker runs steps.
//
// **The same choice a local build makes, and for a stronger reason.** This
// built `exec.NewNative()` directly, so a worker always ran steps behind
// namespaces - not by decision, but because the choice was written down in
// `engine/cli` and this was not that place. A worker runs *other people's*
// Earthfiles, sent to it by a driver it does not control, which is the strongest
// case in this engine for putting a hypervisor between a step and the host; it
// was the one place that could not have one.
//
// Degrades rather than refuses where a machine cannot be built, exactly as a
// local build does, and says which boundary it got - a worker that quietly
// offered less isolation than the operator believes is the failure this reports
// its way out of (I11).
func workerSandbox() (exec.Sandbox, error) {
	root := os.Getenv("EARTH_CACHE_DIR")
	if root == "" {
		return nil, errors.New("set EARTH_CACHE_DIR to where this worker keeps" +
			" its layers" +
			"\n  a worker materialises bases and captures results, so it needs" +
			" a store of its own")
	}

	sb, err := cli.SandboxIn(root)
	if err != nil {
		return nil, fmt.Errorf("this machine cannot run steps: %w", err)
	}

	if _, isVM := sb.(*exec.Firecracker); !isVM {
		fmt.Fprintln(os.Stderr,
			"earth-worker: steps run in namespaces on this machine, not in a"+
				" microVM\n  a worker runs Earthfiles it did not write: a"+
				" machine that can boot one is worth having")
	}

	return sb, nil
}
