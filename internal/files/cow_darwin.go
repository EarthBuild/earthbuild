//go:build darwin

package files

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func copyOnWriteFile(src, dst string) error {
	err := unix.Clonefile(src, dst, 0)
	if err != nil {
		return fmt.Errorf("copy on write file %q to %q: %w", src, dst, err)
	}

	return nil
}

func copyOnWriteDir(src, dst string) error {
	err := unix.Clonefile(src, dst, 0)
	if err != nil {
		return fmt.Errorf("copy on write dir %q to %q: %w", src, dst, err)
	}

	return nil
}
