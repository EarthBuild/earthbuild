//go:build linux

package guest

import "golang.org/x/sys/unix"

// mountBinfmt makes the interpreter register readable.
//
// Already mounted is not an error: the guest may be sharing a mount namespace
// with something that did it first, and EBUSY then means exactly what is
// wanted.
func mountBinfmt() {
	_ = unix.Mount("binfmt_misc", binfmtRegister, "binfmt_misc", 0, "")
}
