//go:build darwin

package exec

import (
	"runtime"
	"strconv"
	"testing"
)

// TestTheSandboxAsksForTheMachinesCores.
//
// **Four, because nobody asked for a number.** `container run` defaults to four
// vCPUs and this never passed `-c`, so a sixteen-core machine ran every `RUN`
// on a quarter of itself. Docker's VM on the same machine takes all sixteen,
// which is most of why a cold `+earthly` measured slower here than under
// BuildKit: the comparison gave one engine four cores and the other sixteen
// for the same `go build`.
//
// Asked for explicitly, so the number is a decision rather than somebody
// else's default, and overridable for a machine that has other work to do.
func TestTheSandboxAsksForTheMachinesCores(t *testing.T) {
	if got := sandboxCPUs(); got != strconv.Itoa(runtime.NumCPU()) {
		t.Errorf("the sandbox asks for %s cores on a %d-core machine",
			got, runtime.NumCPU())
	}

	t.Setenv(EnvSandboxCPUs, "3")

	if got := sandboxCPUs(); got != "3" {
		t.Errorf("the override was ignored: got %s, want 3", got)
	}

	// A machine that has to share is the reason the override exists; a value
	// that is not a count is not one, and the default is better than a refusal.
	t.Setenv(EnvSandboxCPUs, "all of them")

	if got := sandboxCPUs(); got != strconv.Itoa(runtime.NumCPU()) {
		t.Errorf("a nonsense override gave %s rather than falling back", got)
	}
}

// And it is in the sandbox's name, for the reason every other start-time
// setting is: a machine is found and reused by name, so one started with four
// cores must not answer a build that asked for sixteen (E549).
func TestTheCoreCountNamesTheSandbox(t *testing.T) {
	plain := SandboxName("an-image", "/guest", "/store")

	t.Setenv(EnvSandboxCPUs, "2")

	if got := SandboxName("an-image", "/guest", "/store"); got == plain {
		t.Errorf("asking for two cores does not change the sandbox's name (%s),"+
			"\n  so a machine started with another count answers this build", got)
	}
}
