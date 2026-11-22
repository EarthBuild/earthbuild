package guest

import (
	"strings"
	"testing"
)

// A step's own name resolves.
//
// The reference engine calls its sandbox `buildkitsandbox`, writes that name
// into the step's `/etc/hosts`, and the corpus depends on it: two test trees
// `ping -c 1 buildkitsandbox` to check the hosts file is working at all. More
// than the tests, a name that does not resolve is a class of slow build -
// `InetAddress.getLocalHost()`, `gethostbyname(uname -n)` and every configure
// script that reaches for the build host wait for a resolver to say no (E758).
func TestAStepsOwnNameIsInItsHostsFile(t *testing.T) {
	t.Parallel()

	got := hostsFile([]string{"git.example.com 10.0.0.1"})

	for _, want := range []string{
		"127.0.0.1\tlocalhost\n",
		"127.0.0.1\t" + SandboxHost + "\n",
		"10.0.0.1\tgit.example.com\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the hosts file does not contain %q:\n%s", want, got)
		}
	}
}

// A step that declared nothing still gets a hosts file.
//
// **This test asserted the opposite until E768, and the opposite was wrong.**
// A step with no `HOST` entries kept its image's `/etc/hosts`, which does not
// name the sandbox - and `earth-entrypoint.sh` derives the inner build's
// buildkit address from `hostname`, so once the sandbox had a name (E758) the
// inner build dialled one nothing resolved and waited a minute to find out.
//
// The old rule's reasoning survives: what a step resolves by is what the
// Earthfile said, written rather than merged with whatever the image shipped.
// What it missed is that a step is entitled to two names before any Earthfile
// says anything - localhost, and its own.
func TestAStepThatDeclaredNoHostsStillResolvesItsOwnName(t *testing.T) {
	t.Parallel()

	got := hostsFile(nil)

	for _, want := range []string{"127.0.0.1\tlocalhost\n", "127.0.0.1\t" + SandboxHost + "\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("a step declaring nothing cannot resolve %q:\n%s", want, got)
		}
	}
}
