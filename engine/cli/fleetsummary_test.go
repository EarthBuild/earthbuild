package cli

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
)

// A fleet build says how much of it was a fleet.
//
// Without this line there is nothing to check: a fleet that formed, placed
// nothing and built locally prints the same output as one that delegated
// everything, and both exit zero. That is the shape a CI job asserts on - the
// negative half especially, since a silent fall back to local execution is
// exactly what a green build hides (E505).
func TestAFleetBuildSaysHowMuchItDelegated(t *testing.T) {
	t.Parallel()

	got := fleetSummary(fleet.Spend{Delegated: 3, Local: 2})

	for _, want := range []string{"3", "2", "delegated"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q does not mention %q", got, want)
		}
	}
}

// A fleet that delegated nothing says so loudly.
//
// It is the failure worth naming: the workers arrived, the build was slower than
// a local one for having waited, and every step ran on the driver anyway.
func TestAFleetThatDelegatedNothingSaysSo(t *testing.T) {
	t.Parallel()

	got := fleetSummary(fleet.Spend{Delegated: 0, Local: 5})

	if !strings.Contains(got, "0") {
		t.Errorf("%q does not say that nothing was delegated", got)
	}

	if got == "" {
		t.Fatalf("a fleet that delegated nothing printed nothing at all")
	}
}

// A build with no fleet says nothing about fleets.
func TestABuildWithNoFleetSaysNothingAboutOne(t *testing.T) {
	t.Parallel()

	if got := fleetSummary(fleet.Spend{}); got != "" {
		t.Errorf("a build with no fleet printed %q", got)
	}
}
