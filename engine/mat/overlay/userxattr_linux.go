//go:build linux

package overlay

import (
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

// probeOnce holds the answer for the life of the process.
var (
	probeOnce sync.Once
	probeUser bool
)

// needsUserXattr reports whether this process must keep overlayfs's metadata in
// the `user.` namespace.
//
// **Measured, not modelled.** The tempting rule is "userxattr when euid != 0",
// and it is wrong in both directions: inside a mapped user namespace euid *is*
// zero and `trusted.` is still refused, and a privileged container may have
// CAP_SYS_ADMIN without being uid 0. The question the kernel will actually ask
// is whether this process can write a `trusted.` attribute, so that is the
// question asked here - once, against the directory the upper layer will live
// in, so the answer comes from the filesystem that will hold it.
//
// Three outcomes, not two: it works, it is refused, or the filesystem does not
// take extended attributes at all. The third is not "no" - a probe with fewer
// outcomes than the world is how the store's case-sensitivity check reported
// "could not tell" as "case-insensitive" (E97). Here an unanswerable probe
// takes `user.`, which needs no privilege and so cannot be the wrong half of a
// pair that fails silently.
func needsUserXattr(scratch string) bool {
	probeOnce.Do(func() { probeUser = probeUserXattr(scratch) })

	return probeUser
}

// probeUserXattr is needsUserXattr without the memoisation, so a test can ask
// twice.
func probeUserXattr(scratch string) bool {
	dir, err := os.MkdirTemp(scratch, "xattr-probe-")
	if err != nil {
		return true
	}

	defer func() { _ = os.RemoveAll(dir) }()

	p := filepath.Join(dir, "probe")

	err = os.WriteFile(p, nil, 0o600)
	if err != nil {
		return true
	}

	err = unix.Lsetxattr(p, "trusted.overlay.opaque", []byte("y"), 0)

	return err != nil
}
