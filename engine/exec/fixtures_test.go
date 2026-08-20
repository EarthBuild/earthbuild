package exec_test

const (
	// testPlatform is the platform a fixture builds for.
	testPlatform = "linux/arm64"
	// testOtherPlatform is a different one, where only the difference matters.
	testOtherPlatform = "linux/amd64"
	// testArch is this fixture's architecture half.
	testArch = "arm64"
	// testNative names the executor that runs a step on this machine.
	testNative = "native"
	// testLocal names the one that runs it outside a sandbox.
	testLocal = "local"
	// testTool is a program an image carries, relative as a tar entry is.
	testTool = "usr/tool"
	// testHeader is an included file, where the nesting is the point.
	testHeader = "inc/b.h"
)
