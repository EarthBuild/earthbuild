//go:build !linux

package guest

// StopMachine has no machine to stop where a sandbox is not one.
//
// The backends that grant EnvOwnsMachine run a Linux guest; anything else is a
// test or a development build, and a sandbox it did not start is not its to
// stop.
func StopMachine() error { return nil }
