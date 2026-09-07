//go:build linux

package nstest

import (
	"errors"
	"testing"
)

// A child that never started is a machine that cannot, not a test that failed.
//
// `In` re-execs the test inside a user namespace and reports what happened
// there. It could not tell "the kernel refused to make the namespace" from "the
// body ran and the assertions failed" - both arrive as a non-zero exit - so a
// container with `CLONE_NEWUSER` disabled reported twelve failing tests in
// engine/guest, none of which had run. A build container is exactly that
// environment, which is how this was found.
//
// The two are distinguishable: a test binary that ran says so, in `go test`'s
// own vocabulary. No such output means nothing executed, and the exit status is
// about the fork rather than about the engine.
//
// This is deliberately *not* the skip the package was written against. That one
// was "unprivileged overlayfs needs `unshare -Umr`, so the test skips unless
// somebody remembers to type it" - a skip that depends on how the binary was
// invoked. Here the re-exec is automatic and the kernel refuses; no invocation
// would help, and saying so is the honest report.
func TestAnUnstartableChildIsNotAFailingTest(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		out  string
		// unstarted is what unstartable should answer: did nothing run at all?
		unstarted bool
	}{
		{"the kernel refused the namespace", "", true},
		{"the fork failed with a message of its own", "fork/exec: operation not permitted\n", true},
		{"the body ran and failed", "=== RUN   TestX\n--- FAIL: TestX (0.00s)\nFAIL\n", false},
		// False, and this row was wrong first time round. A child that skipped
		// *started*; `In` skips for that separately, on the child's own words.
		// The question here is only whether anything ran, and conflating the
		// two would report a machine that cannot make namespaces and a test
		// that declined to run as the same thing.
		{"the body ran and skipped", "=== RUN   TestX\n--- SKIP: TestX (0.00s)\nPASS\n", false},
		{"the body panicked", "=== RUN   TestX\npanic: nil map\nFAIL\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := unstartable([]byte(tc.out)); got != tc.unstarted {
				t.Errorf("unstartable(%q) = %v, want %v", tc.out, got, tc.unstarted)
			}
		})
	}

	// And the error is carried through, because "this machine cannot" is only
	// useful with the reason attached.
	if reason := whyUnstartable(errors.New("operation not permitted")); reason == "" {
		t.Error("the refusal is reported without saying what refused")
	}
}
