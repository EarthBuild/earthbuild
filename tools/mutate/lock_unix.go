//go:build unix

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockSweep takes the sweep lock for one worktree, or refuses.
//
// **flock rather than a pid file**, because the kernel drops it on any exit
// including SIGKILL - and a sweep is interrupted often enough that a stale lock
// would be the commoner failure of the two.
//
// The lock lives beside the temporary files rather than in the worktree: a
// sweep that left a file behind in the repository would show up as a dirty tree
// in exactly the check somebody runs to find out whether a sweep left anything
// behind.
func lockSweep(root string) (func(), error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	sum := sha256.Sum256([]byte(abs))
	path := filepath.Join(os.TempDir(), "mutate-"+hex.EncodeToString(sum[:8])+".lock")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // a path this function built
	if err != nil {
		return nil, err
	}

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		f.Close()

		return nil, fmt.Errorf("a sweep is already running in %s"+
			"\n  two sweeps in one worktree apply mutants to each other's files, and"+
			"\n  the one that reads a file mid-mutation reports its catalogue entry as"+
			"\n  broken - an entry which is correct, against a file since put back"+
			"\n  wait for it to finish, or sweep in a worktree of its own", abs)
	}

	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
