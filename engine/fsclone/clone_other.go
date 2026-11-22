//go:build !linux

package fsclone

import "os"

// Range is the kernel-side copy Linux has and this platform does not.
//
// False rather than an error, because the caller's answer is the same either
// way: copy the bytes here instead. darwin has `clonefile`, which the export
// path uses directly; there is no equivalent that writes into an already-open
// destination.
func Range(_, _ *os.File, _ int64) bool { return false }
