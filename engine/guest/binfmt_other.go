//go:build !linux

package guest

// mountBinfmt does nothing where there is no binfmt_misc to mount. The reader
// then finds no register and reports that this machine emulates nothing, which
// is true.
func mountBinfmt() {}
