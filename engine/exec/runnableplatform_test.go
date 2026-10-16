package exec

import (
	"strings"
	"testing"
)

// A step for a platform the sandbox cannot execute is refused, saying so.
//
// `fork/exec /bin/sh: exec format error` is what running an amd64 binary on an
// arm64 machine looks like, and it names neither the platform nor the image nor
// the line. The sandbox knows what it can run and the step knows what it wants,
// so the two can be compared before anything is executed.
//
// Only *executing* is refused. Cross-building is legitimate - a target that
// copies files for another architecture works perfectly well - so the check
// belongs where a command is about to run rather than where an image is fetched.
//
// **Against a stated set, never `CheckRunnable`.** The exported entry point asks
// `EmulatedPlatforms`, which reads `/proc/sys/fs/binfmt_misc` - the kernel's own
// register, which is not namespaced and is therefore shared with whatever else
// has run on the machine. A box with qemu registered for amd64 makes this step
// runnable and the assertion false, so the test passed or failed according to
// what some other build had done, which is the worst way for a suite to be
// wrong. `checkRunnableWith` exists for exactly this and says so: "so the
// decision can be tested without a kernel register".
func TestAStepForAnUnrunnablePlatformIsRefused(t *testing.T) {
	t.Parallel()

	err := checkRunnableWith(testPlatform, testOtherPlatform, "Earthfile:7", nil)
	if err == nil {
		t.Fatal("a step for a platform this machine cannot run was accepted")
	}

	// The message is the point: a refusal naming neither platform nor line is
	// the `exec format error` this replaced, one layer up.
	for _, want := range []string{testOtherPlatform, testPlatform, "Earthfile:7"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, err)
		}
	}
}

// The ordinary cases are allowed: the same platform, and one that says nothing.
func TestAMatchingOrUnstatedPlatformRuns(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ have, want string }{
		{testPlatform, testPlatform},
		{testPlatform, ""},
		{"", testOtherPlatform},
		// The variant is not the architecture: arm64/v8 runs arm64 code.
		{testPlatform, "linux/arm64/v8"},
	} {
		err := checkRunnableWith(tc.have, tc.want, "Earthfile:1", nil)
		if err != nil {
			t.Errorf("a sandbox on %q refused a step for %q: %v", tc.have, tc.want, err)
		}
	}
}
