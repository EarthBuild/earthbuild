//go:build !unix

package guest

import (
	"os"
	"syscall"
)

// signalOf has no wait status to read on this platform.
func signalOf(*os.ProcessState) syscall.Signal { return 0 }
