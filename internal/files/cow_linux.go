//go:build linux

package files

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func copyOnWriteFile(src, dst string) (err error) {
	// File paths are provided by the caller; file-utility libraries inherently operate on dynamic paths.
	srcFile, err := os.Open(src) // #nosec G304
	if err != nil {
		return fmt.Errorf("copy on write file %q to %q: open src: %w", src, dst, err)
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("copy on write file %q to %q: stat src: %w", src, dst, err)
	}

	// File paths are provided by the caller; file-utility libraries inherently operate on dynamic paths.
	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode().Perm()) // #nosec G304
	if err != nil {
		return fmt.Errorf("copy on write file %q to %q: open dst: %w", src, dst, err)
	}

	var dstClosed bool

	closeDst := func() error {
		if dstClosed {
			return nil
		}

		dstClosed = true

		return dstFile.Close()
	}

	defer func() {
		closeErr := closeDst()
		if closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
			err = errors.Join(err, fmt.Errorf("copy on write file %q to %q: close dst: %w", src, dst, closeErr))
		}
	}()

	err = unix.IoctlFileClone(int(dstFile.Fd()), int(srcFile.Fd()))
	if err != nil {
		err = fmt.Errorf("copy on write file %q to %q: ioctl: %w", src, dst, err)

		closeErr := closeDst()
		if closeErr != nil {
			err = errors.Join(err, fmt.Errorf("copy on write file %q to %q: close dst: %w", src, dst, closeErr))
		}

		removeErr := os.Remove(dst)
		if removeErr != nil {
			return errors.Join(err, fmt.Errorf("copy on write file %q to %q: remove dst: %w", src, dst, removeErr))
		}

		return err
	}

	return nil
}

func copyOnWriteDir(_, _ string) error {
	return errors.New("copy-on-write directory cloning not supported on Linux")
}
