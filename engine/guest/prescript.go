package guest

import (
	"bytes"
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
)

// defaultPreScript is where a step puts a script for the engine to run before
// starting its daemon, named from inside the step.
//
// The path is `earthly` rather than `earthbuild` because it is an interface an
// Earthfile writes to: `tests/with-docker+pre-script-test` copies a file there,
// and so does every build that has ever used the feature. Renaming it would
// break them for a tidiness nobody asked for.
const defaultPreScript = "/usr/share/earthly/dockerd-wrapper-pre-script"

// envPreScript moves it, and is read from the step's own environment.
const envPreScript = "DOCKER_WRAPPER_PRE_SCRIPT"

// preScriptIn reports the pre-script a step ships, named from inside the step,
// or empty for the usual case of none.
//
// **Absent is not an error, in either form.** Almost no step has one, and a
// step that names a path and ships no file there is asking for nothing - so
// refusing would fail a build over a file whose absence changes nothing. The
// same rule `buildkitd/dockerd-wrapper.sh` follows: it tests for the file and
// carries on without it.
//
// Named but absent does not fall back to the default. A step that moved the
// script did so to run *that*, and running the other one instead would run
// something nobody asked for.
func preScriptIn(stepRoot string, env []string) string {
	at := defaultPreScript

	for _, kv := range env {
		if rest, ok := strings.CutPrefix(kv, envPreScript+"="); ok && rest != "" {
			at = rest
		}
	}

	// `within` rather than a plain Join: the value comes from a step's own
	// environment, so it is an author's string and gets the containment every
	// other one does.
	full, err := within(stepRoot, at)
	if err != nil {
		return ""
	}

	fi, statErr := os.Stat(full)
	if statErr != nil || fi.IsDir() {
		return ""
	}

	return filepath.Clean(at)
}

// runPreScript runs the step's pre-script, if it has one, before its daemon.
//
// **Through the shim, because the script is the step's.** It sits in the step's
// filesystem, expects the step's interpreter, and writes where the step will
// look - so it has to run chrooted, which is what the shim does and what the
// guest cannot do to itself without becoming the step. Re-executing this binary
// with the shim flag is exactly how a step is launched; this is that, with one
// argument.
//
// Before the daemon rather than beside it: the point of the hook is to
// configure what the daemon will find, and `buildkitd/dockerd-wrapper.sh` runs
// it in the same place for the same reason.
//
// A failure fails the step. The script exists to make the daemon's environment
// right, so carrying on after it failed would start a daemon into conditions
// the author said were not ready - and the build would fail later, somewhere
// with less to say about why.
func runPreScript(ctx context.Context, stepRoot string, env []string, shimming bool) error {
	at := preScriptIn(stepRoot, env)
	if at == "" {
		return nil
	}

	if !shimming {
		return fmt.Errorf(
			"this step ships %s and there is no shim to run it in"+
				"\n  the script runs inside the step, which needs EARTH_STEP_SHIM", at)
	}

	// This binary, found the way the step launch finds it: the shim is the
	// guest re-executed, so there is nothing else it could be.
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find this binary to run %s in the step: %w", at, err)
	}

	//nolint:gosec // self is this binary and at is checked to be inside stepRoot
	cmd := osexec.CommandContext(ctx, self, stepShimFlag, stepRoot, "/", at)
	cmd.Env = env

	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		return fmt.Errorf("run %s before this step's daemon: %w\n  %s",
			at, runErr, bytes.TrimSpace(out))
	}

	return nil
}
