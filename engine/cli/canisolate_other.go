//go:build !linux

package cli

// backendCanIsolate reports whether a step can have a daemon of its own here.
//
// It cannot: the sandbox is a VM whose single daemon the blocks of a build
// share, so `--isolate` would be approximated rather than provided - which is
// the substitution §3.4b forbids (E391).
func backendCanIsolate() bool { return false }
