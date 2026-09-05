package main

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// A worker on darwin runs steps in the Apple sandbox.
//
// `cmd/earth-worker` had a backend for Linux and a refusal for everything else,
// so a fleet was Linux-only - a smaller decision than it looked. The Apple
// sandbox is the *local* engine's own backend on this platform, so a darwin
// worker is the same wiring the Linux one does with `exec.NewNative()`: a
// sandbox, a store of its own, and `Available()` asked before joining anything
// (E501).
//
// What it offers is ordinary `linux/arm64` work, exactly as a local build on
// this machine does. Running `LOCALLY` steps *as macOS* is the other half and
// needs the platform to reach placement as something other than `linux/*`; it is
// a plan item and not this.
func TestADarwinWorkerHasASandbox(t *testing.T) {
	t.Setenv("EARTH_CACHE_DIR", t.TempDir())

	sb, err := workerSandbox()
	if err != nil {
		// A machine without the Apple container runtime cannot run steps, and
		// says so rather than joining a fleet it will refuse every step from.
		// That is a legitimate outcome here; a *refusal by platform* is not.
		if strings.Contains(err.Error(), "no worker backend") {
			t.Fatalf("darwin was refused by name rather than by capability: %v", err)
		}

		t.Skipf("this machine cannot run steps: %v", err)
	}

	if sb == nil {
		t.Fatal("no sandbox and no error")
	}

	// A worker's store is its own: two workers on one machine must not share
	// a layer store, and on darwin the VM is named after its mounts - so the
	// store is also what keeps their machines apart (E501).
	if got := sb.StoreDir(); got == "" {
		t.Error("the worker's sandbox has no store, so it has nowhere to" +
			" materialise a base or keep what it captures")
	}
}

// And it insists on being told where that store is.
//
// The same refusal the Linux backend makes, and for the same reason: a worker
// materialises bases and captures results, so a store it did not choose is a
// worker writing into somebody else's.
func TestADarwinWorkerNeedsAStore(t *testing.T) {
	t.Setenv("EARTH_CACHE_DIR", "")

	_, err := workerSandbox()
	if err == nil {
		t.Fatal("a worker with no store was given a sandbox")
	}

	if !strings.Contains(err.Error(), "EARTH_CACHE_DIR") {
		t.Errorf("refused with %q, which does not name what to set", err)
	}
}

var _ exec.Sandbox = (exec.Sandbox)(nil)
