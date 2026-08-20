//go:build !linux

package guest

import "github.com/EarthBuild/earthbuild/engine/trace"

// runObserved runs the step, unobserved.
//
// Seccomp user notification is a Linux facility. On darwin a step runs through a
// different sandbox entirely, and the honest answer is that this platform has no
// observation source for RUN yet - said out loud rather than returned as an
// empty and complete-looking observation, which would serve L2 hits against
// reads nobody looked for (I3, I10).
func runObserved(
	fn func() ([]byte, error), _ func(string) error,
) ([]byte, error, trace.Sightings) {
	out, err := fn()

	return out, err, trace.Unobserved(nil)
}
