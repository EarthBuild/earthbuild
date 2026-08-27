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

// A step that declared nothing still gets no hosts file.
//
// Writing one unconditionally would replace whatever its image ships for every
// step in every build, which is a much larger change than making a name
// resolve - and the image's file is what a step without declarations has always
// resolved by.
func TestAStepThatDeclaredNoHostsStillGetsNoFile(t *testing.T) {
	t.Parallel()

	if got := hostsFile(nil); got != "" {
		t.Errorf("a step declaring nothing got a hosts file:\n%s", got)
	}
}
