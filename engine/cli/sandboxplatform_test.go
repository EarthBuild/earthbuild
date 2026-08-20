package cli_test

import "runtime"

// testPlatform is the platform a sandboxed case builds for.
//
// Always this machine's architecture. Both backends run the guest at native
// speed and neither emulates: Apple's `container` boots an arm64 VM on an arm64
// Mac, and the native backend forks the guest as a child of this process. The
// suite said `linux/arm64` outright, which was true of every machine it had ever
// run on and false of the first x86 one.
func testPlatform() string {
	return "linux/" + runtime.GOARCH
}
