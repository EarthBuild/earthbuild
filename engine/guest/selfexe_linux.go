//go:build linux

package guest

import (
	"fmt"
	"os"
)

// selfExe is the path to re-execute this binary by, for the daemon shim.
//
// Two answers, and each covers the other's failure:
//
//   - `os.Executable()` is the path the binary was started from. It is right
//     whenever it still exists, and it does not depend on `/proc` matching
//     anything.
//   - `/proc/self/exe` is a link the kernel keeps to the running image, so it
//     survives a binary that has been replaced or unlinked - a `go test` binary
//     in a build cache, or a deployment mid-upgrade.
//
// The order is not arbitrary. `/proc/self/exe` is resolved through whichever
// `/proc` is mounted, and a `/proc` that predates the process's pid namespace
// resolves `self` to a different process entirely - which is `fork/exec
// /proc/self/exe: permission denied` rather than anything that names the real
// problem (E376). So the started-from path is preferred, and the kernel's link
// is the fallback for when it has gone.
func selfExe() (string, error) {
	if p, err := os.Executable(); err == nil {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	if _, err := os.Stat("/proc/self/exe"); err != nil {
		return "", fmt.Errorf(
			"find this binary, to re-execute it as the daemon's shim: neither the"+
				" path it was started from nor /proc/self/exe is usable: %w", err)
	}

	return "/proc/self/exe", nil
}
