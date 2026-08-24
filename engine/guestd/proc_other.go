//go:build !linux

package guestd

// procForTracing has nothing to arrange: there is no tracer on this platform.
func procForTracing() {}
