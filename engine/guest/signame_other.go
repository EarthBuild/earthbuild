//go:build !unix

package guest

import "syscall"

// signalName falls back to the description where there is no signal table.
func signalName(sig syscall.Signal) string { return sig.String() }
