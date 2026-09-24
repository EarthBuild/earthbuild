//go:build unix

package guest

import (
	"os"
	"syscall"
)

// signalOf is the signal that ended a process, or zero if it exited normally.
//
// Go flattens a signalled death to exit -1, so the code alone says "this did not
// exit" and nothing about why. The wait status still holds the signal.
func signalOf(st *os.ProcessState) syscall.Signal {
	if st == nil {
		return 0
	}

	ws, ok := st.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return 0
	}

	return ws.Signal()
}
