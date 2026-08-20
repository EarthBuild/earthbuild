package exec

import (
	"errors"
	"fmt"
	"os"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

// agentIn is where a step finds the agent it asked for.
//
// A fixed path, because the *invoking* socket's path is per-invocation and must
// not reach the step's environment either: two builds of one Earthfile would
// then run different commands, and `SSH_AUTH_SOCK` is expanded into what the
// step sees (E466).
const agentIn = "/run/earthbuild/ssh-agent.sock"

// ErrNoAgent reports that a step asked for an agent this invocation has none of.
var ErrNoAgent = errors.New("no ssh agent")

// sshAgent is the mount and the environment a `RUN --ssh` step needs.
//
// **Refused rather than approximated.** A step that asked for the agent and did
// not get it fails inside the sandbox, on whatever it was trying to reach, with
// a message about a host key or a permission - and the cause is that the caller
// had no agent running. Saying so here costs a build and saves the reader the
// wrong search (I10).
func sshAgent(sock string) ([]guest.Mount, map[string]string, error) {
	if sock == "" {
		return nil, nil, fmt.Errorf(
			"%w: this step asks for one with `RUN --ssh`"+
				"\n  start one and add a key - `eval $(ssh-agent)` then `ssh-add`"+
				" - or remove the flag", ErrNoAgent)
	}

	fi, err := os.Stat(sock)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"%w: SSH_AUTH_SOCK is %s and this engine cannot use it: %w",
			ErrNoAgent, sock, err)
	}

	// A socket, not a file somebody exported the variable at. The check is
	// cheap and the failure it prevents is a bind mount of the wrong thing into
	// every step that asked.
	if fi.Mode()&os.ModeSocket == 0 {
		return nil, nil, fmt.Errorf(
			"%w: SSH_AUTH_SOCK is %s, which is not a socket", ErrNoAgent, sock)
	}

	return []guest.Mount{{Sandbox: sock, Target: agentIn}},
		map[string]string{"SSH_AUTH_SOCK": agentIn}, nil
}
