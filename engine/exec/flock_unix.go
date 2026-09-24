//go:build !windows

package exec

import (
	"os"

	"golang.org/x/sys/unix"
)

// tryFlock takes an exclusive lock on a file, or says immediately that it
// cannot.
//
// **Its own file because `unix.Flock` is not everywhere.** Three call sites
// used it directly, so `GOOS=windows go build ./...` failed on the package and
// `+all-binaries` could not produce `earthly.exe` at all - a release path
// broken in a way no test ran, because nothing in this repository cross-builds
// for windows.
func tryFlock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) //nolint:wrapcheck // callers write the diagnosis
}
