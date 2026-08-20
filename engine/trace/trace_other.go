//go:build !linux

// Package trace observes what a step looked at while it ran.
//
// Empty here. Seccomp user notification is a Linux facility, and the
// materialiser on darwin runs steps through a different sandbox entirely - so
// there is nothing to stub, and a stub would be a claim that the same seam
// exists on both. When the Tracer interface lands it will refuse on this
// platform in the ordinary way, with ErrUnimplemented, at the point a caller
// asks for one.
//
// This file exists so the package has files on every platform. A package that
// vanishes outside `//go:build linux` builds under `./...` and breaks the moment
// anything imports it, which is a worse way to find out.
package trace

// Sightings is what a step was seen to look at.
//
// Declared on every platform so a caller can hold one without knowing whether
// this machine can produce it. On a platform with no observation source the only
// honest value is an incomplete one - see Unobserved.
type Sightings struct {
	Paths      []string
	Incomplete bool
	Why        []string
}
