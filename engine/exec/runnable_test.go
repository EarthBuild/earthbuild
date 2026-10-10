package exec_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

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

	got := exec.ExplainExec(in, testPlatform, "Earthfile:7")
	if !errors.Is(got, in) {
		t.Errorf("an ordinary failure was rewritten as %v", got)
	}
}
