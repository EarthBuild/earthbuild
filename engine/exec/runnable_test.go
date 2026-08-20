package exec_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
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
func TestAStepForAnUnrunnablePlatformIsRefused(t *testing.T) {
	t.Parallel()

	err := exec.CheckRunnable(testPlatform, testOtherPlatform, "Earthfile:7")
	if err == nil {
		t.Fatal("a step for a platform this machine cannot run was accepted")
	}

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
		err := exec.CheckRunnable(tc.have, tc.want, "Earthfile:1")
		if err != nil {
			t.Errorf("a sandbox on %q refused a step for %q: %v", tc.have, tc.want, err)
		}
	}
}

// `exec format error` is explained rather than passed on.
//
// It is what the kernel says when a binary is for another architecture, and it
// names neither the binary's platform nor the machine's. A cached image pulled
// before this engine checked architectures is exactly that case: the step asks
// for the sandbox's own platform, so nothing compares them, and the first
// command fails with six words.
//
// Explained where it surfaces, because every route to it ends here - including
// the ones nobody has thought of.
func TestAnExecFormatErrorIsExplained(t *testing.T) {
	t.Parallel()

	err := exec.ExplainExec(
		errors.New(`exec [/bin/sh -c make]: fork/exec /bin/sh: exec format error`),
		testPlatform, "Earthfile:7")
	if err == nil {
		t.Fatal("no error")
	}

	for _, want := range []string{"another architecture", testPlatform, "Earthfile:7"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the explanation does not mention %q:\n%s", want, err)
		}
	}
}

// Any other failure is passed through untouched: a command that exits 1 is not
// a platform problem, and dressing it as one would send the reader away from
// the cause.
func TestAnOrdinaryFailureIsNotExplainedAway(t *testing.T) {
	t.Parallel()

	in := errors.New("exit status 1")

	if got := exec.ExplainExec(in, testPlatform, "Earthfile:7"); got != in {
		t.Errorf("an ordinary failure was rewritten as %v", got)
	}
}
