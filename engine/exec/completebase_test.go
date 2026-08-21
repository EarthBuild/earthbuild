package exec

import (
	"context"
	"testing"
)

// A fault against a base that was assembled whole is a negative lookup.
//
// **Not every miss is a fault.** A step resolving a command walks `PATH`:
// `go version` opens `/go/bin/go`, which is not in the image, before finding
// `/usr/local/go/bin/go`, which is. The tracer stops the syscall on any path
// that is not there, so a base with nothing missing still produces faults - one
// per probe.
//
// For a base that was primed, the honest answer may be a fetch. For a base that
// was assembled whole there is nothing to fetch and nothing missing that should
// be there, so the answer is the one the protocol already has a word for: an
// empty error, meaning the host looked and the file is genuinely absent, and the
// step gets its ENOENT (E289).
//
// Reported as an error instead, this failed a real fleet build: a worker took an
// assignment, ran `go version`, faulted on a `PATH` probe, and refused the step
// it had already fetched a 267MB base for.
func TestAFaultAgainstACompleteBaseIsAbsence(t *testing.T) {
	t.Parallel()

	e := &Executor{}

	e.remember("h1", primedBase{complete: true})

	err := e.FillFor(context.Background(), "h1", "/go/bin/go")
	if err != nil {
		t.Errorf("a complete base reported %v"+
			"\n  nothing is missing from it, so the path is simply not there", err)
	}
}

// A fault against a handle nobody knows is still refused.
//
// It cannot be answered from another step's base, and saying "absent" would tell
// a step that a file it could have had does not exist - which is a wrong build
// rather than a slow one.
func TestAFaultAgainstAnUnknownHandleIsRefused(t *testing.T) {
	t.Parallel()

	e := &Executor{}

	if err := e.FillFor(context.Background(), "nobody", "/x"); err == nil {
		t.Error("a fault against an unknown base was answered")
	}
}

// A primed base with nowhere to fetch from says so.
func TestAPrimedBaseWithNoFetcherSaysSo(t *testing.T) {
	t.Parallel()

	e := &Executor{}

	e.remember("h2", primedBase{into: "/tmp/x"})

	if err := e.FillFor(context.Background(), "h2", "/x"); err == nil {
		t.Error("a primed base with no fetcher answered a fault")
	}
}
