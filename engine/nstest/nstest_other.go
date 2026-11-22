//go:build !linux

// Package nstest re-runs a test inside a user namespace, where one can be made.
//
// This file is the answer for platforms where one cannot: the test says so and
// skips, rather than failing with a mount error that names nothing.
package nstest

import "testing"

// In reports that the body may run directly.
//
// Off Linux there are no user namespaces and nothing that needs one: a test
// calling this either does not reach the overlay path at all, or skips for its
// own reasons. Returning true rather than skipping keeps the decision with the
// caller, which is the only party that knows what it needs.
func In(*testing.T) bool { return true }
