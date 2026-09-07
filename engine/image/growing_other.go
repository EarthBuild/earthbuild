//go:build !linux

package image

import "os"

// noReadahead is Linux's, and the guest is the only place a growing blob is
// read. Elsewhere this is a no-op: the reader is bounded by what the writer has
// confirmed regardless, so the worst case is reading a page twice.
func noReadahead(*os.File) {}
