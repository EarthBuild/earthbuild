package trace

import (
	"os"
	"strings"
	"testing"
)

// SkipIfAlreadyFiltered skips a test that cannot install a filter of its own.
//
// **These tests are seccomp tests, and a seccomp filter is inherited.** Run
// inside a step this engine traces - which is what `+unit-test` does - the
// helper they start is already filtered by the *step's* tracer, so the listener
// it is supposed to install and hand back never arrives. The test then sits in
// `fdpass.RecvFile` waiting for a descriptor nobody is going to send.
//
// The existing skip catches the case where the helper reports a failure; it
// cannot catch this one, because nothing fails - the wait simply does not end.
// So the condition is read off the process instead: `/proc/self/status` reports
// `Seccomp: 2` for a filtered task, which is exactly the fact that makes the
// test impossible here.
//
// Exported so the package's external tests can reach it: a `_test.go` file in
// the package proper is the one place a helper can serve both halves without
// two copies drifting apart.
//
// Measured before this existed: `go test ./engine/trace/...` inside a step ran
// for the full `-timeout 5m` and was killed, twice, and took `+unit-test` from
// 147s to 797s largely on its own (E586, E587).
func SkipIfAlreadyFiltered(t *testing.T) {
	t.Helper()

	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		// No procfs to ask. Not a reason to skip: every other platform reaches
		// the ordinary path, where the helper answers or reports why.
		return
	}

	for line := range strings.SplitSeq(string(b), "\n") {
		rest, ok := strings.CutPrefix(line, "Seccomp:")
		if !ok {
			continue
		}

		if strings.TrimSpace(rest) != "0" {
			t.Skip("this process is already under a seccomp filter, so a filter" +
				" installed here cannot be the one that answers: run these tests" +
				" outside a traced step")
		}

		return
	}
}
