//go:build !linux

package exec

// lostGuest is linux-only: no other backend keeps a console of its own.
func lostGuest(err error, _ Sandbox) error { return err }
