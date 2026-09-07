//go:build unix

package guest

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// signalName is the name a reader can search for.
//
// **`syscall.Signal.String()` gives the description, not the name**: SIGKILL
// renders as "killed", which is a word that appears in a hundred unrelated
// messages and cannot be grepped for. The name is the thing anybody reading a
// failure will type into a search.
func signalName(sig syscall.Signal) string {
	if name := unix.SignalName(sig); name != "" {
		return name
	}

	return sig.String()
}
