//go:build !linux && !darwin

package main

import (
	"errors"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// workerSandbox refuses, by name.
//
// Linux and darwin each have one; everything else does not. Refused at startup
// rather than half-wired: a worker that joined a fleet and then refused every
// step would be worse than one that did not join - the driver would keep sending
// it work and keep getting it back.
//
// Windows is the interesting absence. There is no Linux sandbox to fall back on
// without WSL2 or a VM, so a Windows worker would be a `LOCALLY`-only worker -
// worth having on its own, since a build step that must run on Windows has
// nowhere else to go, and a plan item rather than a gap (E501).
func workerSandbox() (exec.Sandbox, error) {
	return nil, errors.New("this platform has no worker backend yet" +
		"\n  a worker runs steps, which needs a sandbox: macOS has the Apple" +
		" backend and Linux has namespaces" +
		"\n  run the worker on one of those")
}
