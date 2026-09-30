package buildkitd

import (
	"testing"

	"github.com/EarthBuild/earthbuild/internal/engine"
)

// An Apple Container BuildKit is told to stop itself when idle; others are not.
//
// `container` gives its VM no balloon device, so the memory a build touched is
// held until the VM stops. Stopping BuildKit stops the container and so the VM,
// and the cache survives on its volume. Docker and Podman return memory without
// this, and a daemon that vanished under them would only surprise somebody.
func TestOnlyAnAppleBuildkitStopsWhenIdle(t *testing.T) {
	t.Parallel()

	s := Settings{IdleTimeoutS: 1800}

	if got, ok := idleExitEnv(engine.SchemeApple, s); !ok || got != "1800" {
		t.Errorf("apple: got (%q, %v), want (\"1800\", true)", got, ok)
	}

	for _, scheme := range []engine.Scheme{engine.SchemeDocker, engine.SchemePodman} {
		if got, ok := idleExitEnv(scheme, s); ok {
			t.Errorf("%s was told to stop when idle (%q); only Apple needs it", scheme, got)
		}
	}

	if got, ok := idleExitEnv(engine.SchemeApple, Settings{IdleTimeoutS: 0}); ok {
		t.Errorf("a timeout of 0 still set the variable (%q); 0 is the way to turn it off", got)
	}
}

// And changing the timeout restarts BuildKit, like any other setting it reads.
func TestTheIdleTimeoutIsPartOfTheSettingsHash(t *testing.T) {
	t.Parallel()

	a, err := Settings{IdleTimeoutS: 1800}.Hash()
	if err != nil {
		t.Fatal(err)
	}

	b, err := Settings{IdleTimeoutS: 600}.Hash()
	if err != nil {
		t.Fatal(err)
	}

	if a == b {
		t.Error("two idle timeouts hash alike, so changing it would not restart BuildKit")
	}
}
