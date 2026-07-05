// Package files provides utilities for secure and robust filesystem operations.
package files

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Copy copies a file, directory, or symbolic link recursively from src to dst.
// Permissions and executable bits are preserved.
// This serves as the recursive fallback for local artifact saving (SAVE ARTIFACT ... AS LOCAL)
// when hard linking by [os.Link] fails (e.g. cross-device mounts).
func Copy(src, dst string) error {
	errorf := func(format string, args ...any) error {
		return fmt.Errorf("copy %s to %s: "+format, append([]any{src, dst}, args)...)
	}

	srcInfo, err := os.Lstat(src)
	if err != nil {
		return errorf("lstat %s: %w", src, err)
	}

	// Handle symlink
	if srcInfo.Mode()&os.ModeSymlink != 0 {
		var link string

		link, err = os.Readlink(src)
		if err != nil {
			return errorf("read symlink %s: %w", src, err)
		}
		// Remove existing file/symlink at dst if any, to avoid "file exists" error
		err = os.Remove(dst)
		if err != nil && !os.IsNotExist(err) {
			return errorf("remove %s: %w", dst, err)
		}

		err = os.Symlink(link, dst)
		if err != nil {
			return errorf("symlink %s to %s: %w", link, dst, err)
		}

		return nil
	}

	// Handle directory
	if srcInfo.IsDir() {
		// Create the destination directory preserving the source mode
		err = os.MkdirAll(dst, srcInfo.Mode())
		if err != nil {
			return errorf("mkdir all %s: %w", dst, err)
		}

		var entries []os.DirEntry

		entries, err = os.ReadDir(src)
		if err != nil {
			return errorf("read dir %s: %w", src, err)
		}

		for _, entry := range entries {
			srcPath := filepath.Join(src, entry.Name())
			dstPath := filepath.Join(dst, entry.Name())

			err = Copy(srcPath, dstPath)
			if err != nil {
				return err // Already wrapped by the recursive call
			}
		}

		return nil
	}

	// Handle regular file
	srcFile, err := os.Open(src) // #nosec G304
	if err != nil {
		return errorf("open %s: %w", src, err)
	}
	defer srcFile.Close()

	// Open or create the destination file with the same permissions
	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode().Perm()) // #nosec G304
	if err != nil {
		return errorf("open file %s: %w", dst, err)
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return errorf("copy file %s to %s: %w", srcFile, dstFile, err)
	}

	err = dstFile.Close()
	if err != nil {
		return errorf("close %s: %w", dstFile, err)
	}

	return nil
}
