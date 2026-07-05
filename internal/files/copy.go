// Package files provides utilities for secure and robust filesystem operations.
package files

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// Copy copies a file, directory, or symbolic link recursively from src to dst.
// Permissions and executable bits are preserved.
// This serves as the recursive fallback for local artifact saving (SAVE ARTIFACT ... AS LOCAL)
// when hard linking by [os.Link] fails (e.g. cross-device mounts).
func Copy(src, dst string) (err error) {
	errorf := func(format string, args ...any) error {
		return fmt.Errorf("copy %s to %s: "+format, append([]any{src, dst}, args...)...)
	}

	srcInfo, err := os.Lstat(src)
	if err != nil {
		return errorf("lstat %s: %w", src, err)
	}

	// Handle symlink at the root level
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
		err = copyDir(src, dst, srcInfo.Mode())
		if err != nil {
			return errorf("%w", err)
		}

		return nil
	}

	// Handle regular file
	srcFile, err := os.Open(src) // #nosec G304
	if err != nil {
		return errorf("open %s: %w", src, err)
	}
	defer func() {
		closeErr := srcFile.Close()
		if closeErr != nil {
			err = errors.Join(err, errorf("close source file: %w", closeErr))
		}
	}()

	// Open or create the destination file with the same permissions
	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode().Perm()) // #nosec G304
	if err != nil {
		return errorf("open file %s: %w", dst, err)
	}
	defer func() {
		closeErr := dstFile.Close()
		if closeErr != nil {
			err = errors.Join(err, errorf("close destination file: %w", closeErr))
		}
	}()

	_, err = io.Copy(dstFile, srcFile)

	return err
}

func copyDir(src, dst string, mode os.FileMode) (err error) {
	err = os.MkdirAll(dst, mode)
	if err != nil {
		return fmt.Errorf("mkdir all %s: %w", dst, err)
	}

	srcRoot, err := os.OpenRoot(src)
	if err != nil {
		return fmt.Errorf("open root %s: %w", src, err)
	}
	defer func() {
		closeErr := srcRoot.Close()
		if closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close source root: %w", closeErr))
		}
	}()

	dstRoot, err := os.OpenRoot(dst)
	if err != nil {
		return fmt.Errorf("open root %s: %w", dst, err)
	}
	defer func() {
		closeErr := dstRoot.Close()
		if closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close destination root: %w", closeErr))
		}
	}()

	return copyRoot(srcRoot, dstRoot)
}

func copyRoot(srcRoot, dstRoot *os.Root) (err error) {
	dirFile, err := srcRoot.Open(".")
	if err != nil {
		return fmt.Errorf("open directory: %w", err)
	}
	defer func() {
		closeErr := dirFile.Close()
		if closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close directory: %w", closeErr))
		}
	}()

	entries, err := dirFile.ReadDir(-1)
	if err != nil {
		return fmt.Errorf("readdir: %w", err)
	}

	for _, entry := range entries {
		name := entry.Name()

		var info os.FileInfo

		info, err = srcRoot.Lstat(name)
		if err != nil {
			return fmt.Errorf("lstat %s: %w", name, err)
		}

		// Handle symlink
		if info.Mode()&os.ModeSymlink != 0 {
			var link string

			link, err = srcRoot.Readlink(name)
			if err != nil {
				return fmt.Errorf("read symlink %s: %w", name, err)
			}

			err = dstRoot.Remove(name)
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove %s: %w", name, err)
			}

			err = dstRoot.Symlink(link, name)
			if err != nil {
				return fmt.Errorf("symlink %s to %s: %w", link, name, err)
			}

			continue
		}

		// Handle directory recursively
		if info.IsDir() {
			err = copySubDirRoot(srcRoot, dstRoot, name, info.Mode())
			if err != nil {
				return err
			}

			continue
		}

		// Handle regular file
		err = copyFileRoot(srcRoot, dstRoot, name, info)
		if err != nil {
			return err
		}
	}

	return nil
}

func copySubDirRoot(srcRoot, dstRoot *os.Root, name string, mode os.FileMode) (err error) {
	err = dstRoot.Mkdir(name, mode.Perm())
	if err != nil && !os.IsExist(err) {
		return fmt.Errorf("mkdir %s: %w", name, err)
	}

	subSrcRoot, err := srcRoot.OpenRoot(name)
	if err != nil {
		return fmt.Errorf("open root %s: %w", name, err)
	}
	defer func() {
		closeErr := subSrcRoot.Close()
		if closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close sub-source root %s: %w", name, closeErr))
		}
	}()

	subDstRoot, err := dstRoot.OpenRoot(name)
	if err != nil {
		return fmt.Errorf("open root %s: %w", name, err)
	}
	defer func() {
		closeErr := subDstRoot.Close()
		if closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close sub-destination root %s: %w", name, closeErr))
		}
	}()

	err = copyRoot(subSrcRoot, subDstRoot)

	return err
}

func copyFileRoot(srcRoot, dstRoot *os.Root, name string, info os.FileInfo) (err error) {
	srcFile, err := srcRoot.Open(name)
	if err != nil {
		return fmt.Errorf("open %s: %w", name, err)
	}
	defer func() {
		closeErr := srcFile.Close()
		if closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close %s: %w", name, closeErr))
		}
	}()

	dstFile, err := dstRoot.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("openfile %s: %w", name, err)
	}
	defer func() {
		closeErr := dstFile.Close()
		if closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close %s: %w", name, closeErr))
		}
	}()

	_, err = io.Copy(dstFile, srcFile)

	return err
}
