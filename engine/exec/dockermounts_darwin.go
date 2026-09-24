//go:build darwin

package exec

import (
	"errors"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

// dockerFor gives a WITH DOCKER step the sandbox VM's daemon.
//
// The paths are the sandbox image's, and the image is chosen by the plan
// precisely because it has a daemon in it - so nothing here starts or stops
// anything, and the socket on the other end belongs to a machine destroyed when
// the build ends.
//
// **`--isolate` is refused here rather than approximated**, and that was a
// defect until §3.4b was written: an engine that cannot provide the daemon
// provenance a step asked for refuses the step and does not substitute another
// (I14).
//
// The reasoning that had it approximated is half right. The VM's daemon dies
// with the build, so it holds nothing an *earlier build* left - which is exactly
// why a bare block is safe here and refused on Linux. But it is not destroyed
// between the blocks of one build, so it does hold whatever an earlier block
// loaded, and an isolated block is cached (E381). A key claiming an empty daemon
// against an execution that saw another block's images is a wrong build reported
// as a hit, and one Earthfile with two blocks reaches it (E391).
// The cache directory is the Linux backend's business: there a step's daemon
// gets one of its own, and here every block shares the VM's, so there is
// nothing to point at a directory. Named rather than dropped because the two
// backends implement one signature (revive unused-parameter).
func dockerFor(isolate bool, _, _ string) (dockerPlan, error) {
	if isolate {
		return dockerPlan{}, errors.New(
			"WITH DOCKER --isolate asks for a daemon of this step's own, and this" +
				"\n  backend has only the sandbox VM's, which the blocks of a build share" +
				"\n  the VM's daemon is destroyed when the build ends, so a plain" +
				"\n  WITH DOCKER is unaffected by earlier builds and needs no flag" +
				"\n  a daemon per step is the native backend's: use the `earth-native` binary")
	}

	return dockerPlan{
		Inherit: true,
		Mounts: []guest.Mount{
			{Sandbox: dockerClientPath, Target: dockerClientPath, ReadOnly: true},
			{Sandbox: dockerPluginDir, Target: dockerPluginDir, ReadOnly: true},
			{Sandbox: dockerSocketPath, Target: dockerSocketPath},
		},
	}, nil
}
