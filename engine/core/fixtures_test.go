package core_test

import "strconv"

// Names for the strings these tests repeat, and a helper for the ones that vary
// only by a line number.
//
// `goconst` asks for a constant wherever a literal appears three times or more,
// and in a table-driven suite that is most of the fixture vocabulary. Two shapes
// deserve different answers.
//
// A **value with a meaning** gets a name: the base image, the shell a step runs,
// the source file a copy names. When one changes, it changes here, and E175
// measured what that is worth - the base image alone appeared eighteen times
// across two packages.
//
// A **source location** does not. `Earthfile:2` is a line number quoted back in
// an assertion, and `earthfileLine2` would be a constant whose name is its
// value. `at(2)` says what it is, removes the whole family at once, and reads
// better than either.
func at(line int) string { return "Earthfile:" + strconv.Itoa(line) }

const (
	// testStep is a step name with no meaning beyond being one.
	testStep = "test"
	// testImage is the base a fixture graph stands on. Short, because these
	// tests never pull it - the scheduler and the key derivations do not care
	// what an image reference resolves to.
	testImage = "alpine"
	// testCommand is the command a fixture step runs.
	testCommand = "make"
	// testSource is the file a fixture copy names, and testSourcePath the same
	// file inside a tree.
	testSource     = "main.c"
	testSourcePath = "src/main.c"
	// testDir is a directory a fixture copies from.
	testDir = "src"
	// testLocal is the name of a local context in a fixture graph.
	testLocal = "local"

	// Paths a fixture observation reads. Named because an observed-input test
	// says the same path in the observation, in the base and in the assertion,
	// and a typo in one of the three is a test that passes for the wrong reason.
	testReadPath   = "/src/main.c"
	testHeaderPath = "/opt/foo.h"
	testIncludeDir = "/usr/include"
	testPluginDir  = "/plugins"
	testFlagPath   = "/etc/feature-flag"
	testGitHead    = ".git/HEAD"
	testLockPath   = "Cargo.lock"
	testArch       = "arm64"

	// Step names in a fixture chain, in order.
	testTop  = "top"
	testMid  = "mid"
	testLeaf = "step"
)

const (
	// testOS and testOtherOS are two platforms, where only their difference matters.
	testOS      = "linux"
	testOtherOS = "darwin"
	// testArch2 is the architecture that is not this machine's.
	testArch2 = "amd64"

	// testHeaderFile is a header a step reads out of the system include path.
	testHeaderFile = "/usr/include/foo.h"
	// testRustSource is a source file, where the language is beside the point.
	testRustSource = "src/parser.rs"
	// testSite is a source location, as a prediction is keyed on.
	testSite = "./Earthfile:17"
	// testFailure is what a step that is meant to fail says.
	testFailure = "this fails"
	// testTrue is the word an Earthfile condition evaluates to.
	testTrueWord = "true"
	// testRunMake is the command a fixture runs when the command is not the point.
	testRunMake = "RUN make"
	// testCleanup is a step that runs after a failure.
	testCleanup = "cleanup"
	// testBase is the step everything else is layered on.
	testBase = "base"
	// testFirstKey is a key where only its being distinct from another matters.
	testFirstKey = "aaa"
)

const (
	// testHostClass names the class of worker a step is placed on.
	testHostClass = "mac"
)

const (
	// testCopySrc and testCopyDst are a copy's two ends, where only their being
	// two paths matters.
	testCopySrc = "/src"
	testCopyDst = "/dst"
)
