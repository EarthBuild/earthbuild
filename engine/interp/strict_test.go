package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `--strict` disallows the constructs that make a build unrepeatable.
//
// **The flag was accepted and did nothing.** Its whole purpose is to refuse
// what cannot be reproduced, so silently honouring none of it is worse than
// ignoring a cache flag: the author believes the check ran. `--ci` implies it,
// so a CI pipeline asking for repeatability was getting the ordinary rules.
//
// The semantics are the reference's, so an Earthfile that builds under one
// engine's `--strict` builds under the other's: LOCALLY and interactive steps
// are the two it withholds.
func TestStrictRefusesWhatCannotBeReproduced(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, src, says string
	}{
		{
			name: "LOCALLY",
			src:  "build:\n    LOCALLY\n    RUN ./release.sh\n",
			says: "LOCALLY",
		},
		{
			name: "an interactive step",
			src:  "build:\n    FROM alpine:3.20\n    RUN --interactive sh\n",
			says: "--interactive",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+"\n"+tc.src, "build", interp.WithStrict(true))
			if err == nil {
				t.Fatalf("%s was accepted under --strict", tc.name)
			}

			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("the refusal does not name %s: %v", tc.says, err)
			}

			// It has to say which flag withheld it, or the author is left
			// looking for a defect in an Earthfile that is fine.
			if !strings.Contains(err.Error(), "--strict") {
				t.Errorf("the refusal does not name --strict: %v", err)
			}
		})
	}
}

// Without the flag, both are ordinary. Strict is a choice the invocation makes,
// not a rule the engine holds.
func TestWithoutStrictBothAreOrdinary(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+"\nbuild:\n    LOCALLY\n    RUN ./release.sh\n", "build")
	if err != nil {
		t.Errorf("LOCALLY was refused with no --strict: %v", err)
	}
}
