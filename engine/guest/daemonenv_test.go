package guest

import (
	"slices"
	"testing"
)

// The daemon does not inherit a runtime directory belonging to the machine.
//
// `WITH DOCKER` starts a dockerd beside the step, and it inherited the whole
// environment of whatever invoked the engine. On a GitHub runner that includes
// `XDG_RUNTIME_DIR=/run/user/1001`, which exists on the runner and nowhere in a
// step - so `docker run -t` asked runc for a console socket, runc put it under
// that directory, and the daemon answered:
//
//	failed to create OCI runtime console socket:
//	  stat /run/user/1001: no such file or directory
//
// The step saw exit 127 and no output of its own, which is why it read as a
// missing `docker` binary for three CI rounds. It reproduces anywhere by
// exporting that one variable, and nowhere without it (E963).
//
// Everything else is kept. A proxy setting is how a build reaches a registry
// from a corporate network, and a daemon started without one fails in a way this
// engine cannot explain - so the rule is a named variable and not a policy of
// starting clean.
func TestTheDaemonDropsTheInvokersRuntimeDirectory(t *testing.T) {
	t.Parallel()

	got := daemonEnv([]string{
		"PATH=/usr/bin",
		"XDG_RUNTIME_DIR=/run/user/1001",
		"HOME=/root",
		"HTTPS_PROXY=http://proxy:3128",
	})

	want := []string{"PATH=/usr/bin", "HOME=/root", "HTTPS_PROXY=http://proxy:3128"}

	if !slices.Equal(got, want) {
		t.Errorf("the daemon is given %q, want %q", got, want)
	}

	// An environment without it is handed over unchanged, so the common case
	// costs nothing and cannot reorder anything.
	plain := []string{"PATH=/usr/bin", "HOME=/root"}
	if !slices.Equal(daemonEnv(plain), plain) {
		t.Errorf("an environment with no runtime directory was changed: %q", daemonEnv(plain))
	}
}
