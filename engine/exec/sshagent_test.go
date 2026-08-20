package exec

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// A step that asks for the agent gets it at a fixed path.
//
// The path inside the step is fixed because the *invoking* socket's path is
// per-invocation: `SSH_AUTH_SOCK` is expanded into what the step sees, so two
// builds of one Earthfile would otherwise run different commands (E466).
func TestTheAgentArrivesAtAFixedPath(t *testing.T) {
	t.Parallel()

	sock := listening(t)

	mounts, env, err := sshAgent(sock)
	if err != nil {
		t.Fatal(err)
	}

	if len(mounts) != 1 || mounts[0].Sandbox != sock || mounts[0].Target != agentIn {
		t.Errorf("mounted %+v, want the caller's socket at %s", mounts, agentIn)
	}

	if env["SSH_AUTH_SOCK"] != agentIn {
		t.Errorf("SSH_AUTH_SOCK is %q, and the step finds the agent at %s",
			env["SSH_AUTH_SOCK"], agentIn)
	}
}

// A step that asks for an agent nobody is running is refused, saying so.
//
// The alternative is worse than a refusal: the step fails inside the sandbox on
// whatever it was reaching for, with a message about a host key or a permission,
// and the reader searches the wrong system.
func TestAskingForAnAgentNobodyIsRunning(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, sock string }{
		{"nothing at all", ""},
		{"a path with nothing there", filepath.Join(t.TempDir(), "absent")},
		{"a file that is not a socket", written(t)},
	} {
		_, _, err := sshAgent(tc.sock)
		if err == nil {
			t.Errorf("%s: accepted", tc.name)

			continue
		}

		if !errors.Is(err, ErrNoAgent) {
			t.Errorf("%s: refused with %q, which is not the no-agent refusal",
				tc.name, err)
		}
	}
}

// listening is a real unix socket, because the check is about the mode bits.
//
// **In a short directory, not `t.TempDir()`.** A unix socket's path is capped at
// 104 bytes on darwin and `t.TempDir()` spends most of that on the test's own
// name, so binding failed with `invalid argument` and this skipped - which read
// as a pass for an hour, and let a mutant that returned no mount at all survive
// (E466). A skip and a pass are the same word to everything that reads the
// output.
func listening(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "eb")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	path := filepath.Join(dir, "s")

	l, err := net.Listen("unix", path)
	if err != nil {
		// Not a skip. Every machine this runs on can make a unix socket, and
		// one that cannot is a fact worth a failure rather than a silence.
		t.Fatalf("could not make a unix socket at %s: %v", path, err)
	}

	t.Cleanup(func() { _ = l.Close() })

	return path
}

func written(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "not-a-socket")

	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}
