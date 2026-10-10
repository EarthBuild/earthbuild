//go:build !linux

package guest

import "syscall"

// prepareShim has nothing to prepare off Linux, where `cannotRunDaemon` has
// already refused any step that asked for a daemon. The shim still execs, so the
// tests that assert the launch actually starts something can run here.
func prepareShim() error { return nil }

// namespaced is the identity off Linux, for the same reason.
func namespaced(a *syscall.SysProcAttr) *syscall.SysProcAttr { return a }
