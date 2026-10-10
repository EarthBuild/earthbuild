//go:build linux

package cli

// backendCanIsolate reports whether a step can have a daemon of its own here.
//
// It can: the sandbox filesystem is this machine's and the guest starts one per
// step (E364-E386).
func backendCanIsolate() bool { return true }
