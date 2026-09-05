//go:build !linux

package guest

import (
	"os"

	"github.com/EarthBuild/earthbuild/engine/trace"
)

// runObserved runs the step, unobserved.
//
// Seccomp user notification is a Linux facility. On darwin a step runs through a
// different sandbox entirely, and the honest answer is that this platform has no
// observation source for RUN yet - said out loud rather than returned as an
// empty and complete-looking observation, which would serve L2 hits against
// reads nobody looked for (I3, I10).
func runObserved(
	fn func() ([]byte, error), _ func(string) error, _ func(),
) ([]byte, trace.Sightings, error) {
	out, err := fn()

	return out, trace.Unobserved(nil), err
}

// runObservedViaShim runs the step, unobserved, for the same reason.
//
// The shim hand-off is a seccomp arrangement, so there is nothing here for it to
// hand over. The channel is nil, and the closure is written to expect that.
func runObservedViaShim(
	fn func(channel *os.File) ([]byte, error), _ func(string) error, _ func(),
) ([]byte, trace.Sightings, error) {
	out, err := fn(nil)

	return out, trace.Unobserved(nil), err
}

// pinChoice reports that nothing is pinned.
//
// CPU affinity is a Linux facility, and the shim arrangement that would carry it
// to the step does not exist here either. Answering "no" keeps the caller free of
// build tags for a decision that has one possible answer on this platform.
func pinChoice() (int, bool) { return 0, false }
