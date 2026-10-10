package cli

import "os"

// callersTerminal is the terminal this invocation can offer an interactive step,
// or nil.
//
// Opening `/dev/tty` succeeds only for a process with a **controlling**
// terminal, which is what the path means - and it hands back the descriptor
// itself, which is what a step needs. A check on whether stdin is a character
// device would answer yes for a pipe from another program's pty and for
// `/dev/null` on some systems, and would then have to find the terminal
// separately.
//
// Nil is the ordinary case, not a failure: a CI job, a cron entry and a build
// with its output piped all have nowhere to prompt, and `RUN --interactive` is
// refused for them as a capability the invocation did not provide (E195).
//
// The caller owns the file and closes it; the engine hands the descriptor to a
// step and keeps no copy.
func callersTerminal() *os.File {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil
	}

	return f
}

// HasCallersTerminal reports whether this process has one.
//
// Exported for a test that must run in a *child* with a controlling terminal:
// `go test` has none, so the accepting branch cannot be reached from inside the
// test process, and asserting it from outside needs the question asked in
// there.
func HasCallersTerminal() bool {
	f := callersTerminal()
	if f == nil {
		return false
	}

	_ = f.Close()

	return true
}
