//go:build darwin

package files

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func clone(src, dst string) error {
	err := unix.Clonefile(src, dst, 0)
	if err != nil {
		return fmt.Errorf("clone %q to %q: %w", src, dst, err)
	}

	return nil
}
