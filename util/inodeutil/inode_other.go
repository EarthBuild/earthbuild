//go:build !windows

package inodeutil

import (
	"syscall"
)

// GetInodeBestEffort returns an inode if available, or 0 on failure.
func GetInodeBestEffort(path string) uint64 {
	var (
		stat  syscall.Stat_t
		inode uint64
	)

	err := syscall.Stat(path, &stat)
	if err == nil {
		inode = stat.Ino
	}

	return inode
}
