//go:build linux

package files

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func clone(src, dst string) (err error) {
	errorf := func(format string, args ...any) error {
		return fmt.Errorf("clone %q to %q: "+format, append([]any{src, dst}, args...)...)
	}

	// File paths are provided by the caller; file-utility libraries inherently operate on dynamic paths.
	srcFile, err := os.Open(src) // #nosec G304
	if err != nil {
		return errorf("open src: %w", err)
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return errorf("stat src: %w", err)
	}

	if srcInfo.IsDir() {
		return errorf("cannot clone a directory")
	}

	// File paths are provided by the caller; file-utility libraries inherently operate on dynamic paths.
	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode().Perm()) // #nosec G304
	if err != nil {
		return errorf("open dst: %w", err)
	}
	defer func() {
		closeErr := dstFile.Close()
		if closeErr != nil {
			err = errors.Join(err, errorf("close dst: %w", closeErr))
		}
	}()

	err = unix.IoctlFileClone(int(dstFile.Fd()), int(srcFile.Fd()))
	if err != nil {
		err = errorf("ioctl: %w", err)

		removeErr := os.Remove(dst)
		if removeErr != nil {
			return errors.Join(err, errorf("remove dst: %w", removeErr))
		}

		return err
	}

	return nil
}
