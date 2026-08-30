//go:build !linux

package guest

// openStepNet gives a step no network namespace of its own.
//
// Network namespaces are a Linux mechanism. On macOS the guest is already
// inside a Linux VM and it is *that* guest - built for linux - which answers
// this, so the case this file covers is a platform with neither.
func openStepNet() (path string, done func(), why string) {
	return "", func() {}, ""
}
