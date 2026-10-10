//go:build !unix

package store

import (
	"fmt"
	"os"
)

// freeOn has no answer where the platform exposes none through this package.
//
// An error rather than a zero: zero free space is a fact a caller would act on,
// and "I could not find out" is not that fact. `FullHint` prints the figure only
// when it has one, so the diagnostic degrades to saying less rather than to
// saying something untrue (I11).
func freeOn(path string) (uint64, error) {
	return 0, fmt.Errorf("this platform does not report free space for %s", path)
}

// occupies falls back to what the file holds.
//
// Blocks are a unix stat field, so what a file costs the disk is not available
// here and its contents are the closest honest answer - an under-estimate, which
// makes a collector free less than it meant to rather than delete more (E574).
func occupies(fi os.FileInfo) uint64 { return apparent(fi) }
