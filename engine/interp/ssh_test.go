package interp_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `RUN --ssh` asks for the invoking user's ssh agent.
//
// `tests/ssh.earth` states the contract in two lines:
//
//	RUN test -z "$SSH_AUTH_SOCK"
//	RUN --ssh test -n "$SSH_AUTH_SOCK" && ssh-add -l | grep 'rsa-key-from-...'
//
// - without the flag a step has no agent, and with it the agent answers. It is
// how a build reaches a private dependency without a key ever being written into
// an image (E466).
//
// **The flag is in the operation and the socket's path is not.** A path like
// `/tmp/ssh-XXXX/agent.1234` is per-invocation, and putting it in the key would
// make the same build key differently in every session - so the operation says
// *that* an agent is wanted and the executor finds it, exactly as the docker
// socket is resolved.
func TestRunSSHAsksForTheAgent(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN --ssh ssh-add -l\n", testMain)
	if err != nil {
		t.Fatalf("RUN --ssh was refused: %v", err)
	}

	var found bool

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpExec {
			continue
		}

		found = true

		if !n.Op.SSH {
			t.Error("the step does not ask for the agent")
		}
	}

	if !found {
		t.Fatal("no step in the plan")
	}
}

// A step that did not ask does not get it.
func TestAStepWithoutTheFlagHasNoAgent(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN ssh-add -l\n", testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec && n.Op.SSH {
			t.Error("a step that did not ask for the agent was given one")
		}
	}
}

// Asking for it is part of what the step is.
//
// Two steps running one command, one with the agent and one without, are not the
// same step: the one with it can reach what the other cannot. The
// key-coverage guard walks every field of `ir.Op`, so this is what it would
// catch - and it is asserted directly because the flag is the whole feature.
func TestTheAgentIsPartOfTheKey(t *testing.T) {
	t.Parallel()

	with := plan(t, versioned+"\nmain:\n    FROM alpine:3.22\n    RUN --ssh make\n")
	without := plan(t, versioned+"\nmain:\n    FROM alpine:3.22\n    RUN make\n")

	if with == without {
		t.Error("a step with the agent and one without key the same")
	}
}

func plan(t *testing.T, src string) ir.NodeID {
	t.Helper()

	p, err := interp.Build(src, testMain)
	if err != nil {
		t.Fatal(err)
	}

	return p.Graph.Root.ID()
}
