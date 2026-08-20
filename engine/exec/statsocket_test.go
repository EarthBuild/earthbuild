package exec

import (
	"os"
	"path/filepath"
	"testing"
)

// A socket is found by asking whether it exists, not whether it can be run.
//
// The bug this pins was written and nearly shipped: the check for an inheritable
// daemon used `lookHostDocker`, which is `exec.LookPath`. A unix socket is not
// executable, so the answer would have been "nothing to inherit" on every
// machine and a bare block would have silently started its own daemon instead of
// sharing - the default the whole design turns on, quietly not happening.
//
// The two calls read almost identically at a call site. This test is the
// difference between them, written down.
func TestASocketIsFoundByExistingNotByBeingExecutable(t *testing.T) {
	t.Parallel()

	// A plain, non-executable file stands in for the socket: what matters is
	// that it exists and cannot be run, which is true of both.
	at := filepath.Join(t.TempDir(), "docker.sock")
	if err := os.WriteFile(at, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if !statSocket(at) {
		t.Error("a socket that is there was reported missing")
	}

	if _, ok := lookHostDocker(at); ok {
		t.Error("lookHostDocker answered yes for a non-executable file; if that" +
			" ever becomes true, the two checks are interchangeable and this" +
			" test is the thing that noticed")
	}

	if statSocket(filepath.Join(t.TempDir(), "absent")) {
		t.Error("a socket that is not there was reported present")
	}
}
