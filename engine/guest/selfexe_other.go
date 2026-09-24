//go:build !linux

package guest

import "os"

// selfExe is the path to re-execute this binary by, for the daemon shim.
//
// No `/proc` here, so the started-from path is all there is - acceptable
// because `cannotRunDaemon` refuses a daemon off Linux anyway, and what remains
// is the unit tests, which run from a binary that is still where it was.
func selfExe() (string, error) { return os.Executable() } //nolint:wrapcheck // os reports this verbatim
