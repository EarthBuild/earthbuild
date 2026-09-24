package guest_test

// Names for the strings these fixtures repeat.
//
// A copy test names the layer it copies from in the request, in the store and in
// the assertion, and a typo in one of the three is a test that passes for the
// wrong reason - which is the argument for a name rather than the linter's.
const (
	// testTrue is the command a step runs when the point is that it ran.
	testTrue = "true"
	// testShell is the shell a fixture step runs.
	testShell = "/bin/sh"
)
