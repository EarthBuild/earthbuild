//go:build !linux

package guest

// cannotRunDaemon says why a daemon cannot be run here, or nothing.
//
// Not on this platform: the daemon is started inside the step's namespaces, and
// there are none. On macOS the sandbox is a Linux VM and it is *that* guest -
// built for linux - which answers this.
func cannotRunDaemon() string {
	return "a step's namespaces are a Linux mechanism and this guest is not running on Linux"
}
