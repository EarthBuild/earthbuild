//go:build !darwin && !linux && !windows

package files

import (
	"errors"
	"fmt"
	"io"
	"os"
)

func copyOnWrite(src, dst string) (err error) {
	// File paths are provided by the caller; file-utility libraries inherently operate on dynamic paths.
	srcFile, err := os.Open(src) // #nosec G304
	if err != nil {
		return fmt.Errorf("copy on write %q to %q: open src: %w", src, dst, err)
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("copy on write %q to %q: stat src: %w", src, dst, err)
	}

	if srcInfo.IsDir() {
		return fmt.Errorf("copy on write %q to %q: cannot copy on write a directory", src, dst)
	}

	// File paths are provided by the caller; file-utility libraries inherently operate on dynamic paths.
	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode().Perm()) // #nosec G304
	if err != nil {
		return fmt.Errorf("copy on write %q to %q: open dst: %w", src, dst, err)
	}
	defer func() {
		closeErr := dstFile.Close()
		if closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
			err = errors.Join(err, fmt.Errorf("copy on write %q to %q: close dst: %w", src, dst, closeErr))
		}
	}()

	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		err = fmt.Errorf("copy on write %q to %q: copy: %w", src, dst, err)

		closeErr := dstFile.Close()
		if closeErr != nil {
			err = errors.Join(err, fmt.Errorf("copy on write %q to %q: close dst: %w", src, dst, closeErr))
		}

		removeErr := os.Remove(dst)
		if removeErr != nil {
			return errors.Join(err, fmt.Errorf("copy on write %q to %q: remove dst: %w", src, dst, removeErr))
		}

		return err
	}

	return nil
}
