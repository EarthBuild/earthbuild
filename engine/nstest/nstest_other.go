//go:build !linux

package nstest

import "testing"

// In reports that the body may run directly.
//
// Off Linux there are no user namespaces and nothing that needs one: a test
// calling this either does not reach the overlay path at all, or skips for its
// own reasons. Returning true rather than skipping keeps the decision with the
// caller, which is the only party that knows what it needs.
func In(*testing.T) bool { return true }
