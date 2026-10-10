package guest

// Names for the strings the internal fixtures repeat.
//
// Separate from the external test package's file because a test constant is
// only visible in its own package, and this one holds the copy tests - which
// name the layer they copy from in the request, in the store and in the
// assertion. A typo in one of the three is a test that passes for the wrong
// reason, which is the argument for a name rather than the linter's.
const (
	// testSrcLayer is the layer a fixture copies out of.
	testSrcLayer = "src-layer"
	// testNewer marks the version of a file a later layer wrote.
	testNewer = "newer"
)

const (
	// testOlder marks the version of a file an earlier layer wrote.
	testOlder = "older"
	// testShell is the interpreter a step's command is handed to.
	testShell = "/bin/sh"
	// testMissingWord is what a shell says of a path that is not there. Matched
	// loosely on purpose: the wording differs between shells, and the test that
	// uses it asserts the refusal reaches the caller, not its phrasing.
	testMissingWord = "does not exist"
)
