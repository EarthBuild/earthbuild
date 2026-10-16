//go:build !darwin && !linux

package cli_test

import "testing"

// requireSandbox skips: this platform has no backend, and `sandbox_other.go`
// says so at run time.
func requireSandbox(t *testing.T) {
	t.Helper()

	t.Skip("no sandbox backend on this platform")
}

func sandboxHostsDocker() bool { return false }
