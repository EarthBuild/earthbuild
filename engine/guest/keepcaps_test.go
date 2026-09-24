package guest

import (
	"slices"
	"testing"
)

// A privileged step keeps its capabilities across the USER switch.
//
// `setuid` away from root clears every capability, which is right for an
// ordinary step and wrong for one that asked for privilege. Under buildkit the
// same `RUN --privileged` with `USER bambi` runs as uid 1000 and can still write
// a root-owned directory - measured, both engines, one machine - because
// privilege there is capabilities rather than a uid. Here it was a uid, so the
// step lost CAP_DAC_OVERRIDE and `tee earthly.output` failed in the working
// directory the harness had just been given (E940).
//
// Gated on the flag rather than kept always. Every step in this engine is
// namespace-root and holds every capability already, so keeping them
// unconditionally would hand a plain `USER nobody` step powers buildkit does not
// - accepting something not implemented, which is the expensive half of E34's
// asymmetry.
func TestCapabilitiesAreKeptOnlyForAPrivilegedUserStep(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		req  Request
		want bool
	}{{
		name: "a step that changes user without asking for privilege",
		req:  Request{User: "bambi"},
	}, {
		name: "a privileged step that stays root has nothing to carry",
		req:  Request{Privileged: true},
	}, {
		name: "a privileged step that changes user",
		req:  Request{User: "bambi", Privileged: true},
		want: true,
	}, {
		name: "a step that asked for neither",
		req:  Request{},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := keepCapsEnv(tc.req)

			if (len(got) > 0) != tc.want {
				t.Fatalf("the shim is told %v, and keeping capabilities here is %v", got, tc.want)
			}

			// The name the shim reads, or the instruction is written and never
			// heard.
			if tc.want && !slices.Contains(got, EnvStepKeepCaps+"=1") {
				t.Errorf("the shim is told %v, want %s=1", got, EnvStepKeepCaps)
			}
		})
	}
}
