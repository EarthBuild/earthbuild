package guest

import (
	"strings"
	"testing"
)

// The shim's own variables never reach the step.
//
// They are how the guest tells the shim what to do - which user to become,
// where the tracer's descriptor is, whether to set HOME - and a step that could
// see them would be reading this engine's internals as part of its own
// environment. That is ambient state no key describes (I3), and it would differ
// between a step that names a user and one that does not.
//
// Written when EARTH_STEP_HOME was added (E865), because the strip list had no
// test and a fourth variable was about to be added to it by copying the third.
func TestTheShimsOwnVariablesDoNotReachTheStep(t *testing.T) {
	t.Parallel()

	internal := []string{EnvStepTraceFD, EnvStepTracePin, EnvStepUser, EnvStepHome}

	in := []string{"PATH=/usr/bin", "HOME=/home/somebody"}
	for _, name := range internal {
		in = append(in, name+"=something")
	}

	out := withoutShimVars(in)

	for _, kv := range out {
		for _, name := range internal {
			if strings.HasPrefix(kv, name+"=") {
				t.Errorf("%s reached the step: %q", name, kv)
			}
		}
	}

	// And the step's own environment survives, or this would pass by deleting
	// everything.
	var kept int

	for _, kv := range out {
		if kv == "PATH=/usr/bin" || kv == "HOME=/home/somebody" {
			kept++
		}
	}

	if kept != 2 {
		t.Errorf("the step's own environment did not survive: %q", out)
	}
}
