//go:build !linux

package main

// procForTracing has nothing to arrange: there is no tracer on this platform.
func procForTracing() {}
