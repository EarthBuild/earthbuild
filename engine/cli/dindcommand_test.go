package cli

import (
	"strings"
	"testing"
)

// The docker sandbox makes /proc/sys writable before dockerd starts.
//
// dockerd is the VM's main process, and it enables IP forwarding on start. On
// Apple's container 1.4.1 /proc/sys is read-only even with `--cap-add ALL`, so
// dockerd exited 1 within two seconds, the machine went down with it, and the
// guest exec'd into it was SIGKILLed mid-step: every WITH DOCKER build on a Mac
// ended `guest connection lost: EOF` (exit status 137).
//
// CAP_SYS_ADMIN is enough to remount it read-write, and the dind entrypoint
// runs a command that is not a flag as given - so a shell remounts, then execs
// the entrypoint with dockerd's flags.
func TestTheDockerSandboxMakesProcSysWritableFirst(t *testing.T) {
	t.Parallel()

	cmd := dockerSandboxCommand()
	script := strings.Join(cmd, " ")

	remount := strings.Index(script, "mount -o remount,rw /proc/sys")
	daemon := strings.Index(script, "exec dockerd-entrypoint.sh --feature containerd-snapshotter=true")

	if len(cmd) != 3 || cmd[0] != "sh" || cmd[1] != "-c" || remount < 0 || daemon < remount {
		t.Errorf("the docker sandbox does not remount /proc/sys before starting dockerd: %q", cmd)
	}
}
