// Package files provides utilities for secure and robust filesystem operations.
package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/EarthBuild/earthbuild/internal/telemetry/semconv"
	"go.opentelemetry.io/otel/trace"
)

// copyBuffer is 128KB, which is the optimal size determined by benchmarks.
// It balances system call overhead reductions with L1/L2/L3 cache locality.
type copyBuffer [128 * 1024]byte

var copyBufferPool = sync.Pool{
	New: func() any {
		return new(copyBuffer)
	},
}

// copyStream copies from src to dst using a buffer from copyBufferPool.
func copyStream(dst io.Writer, src io.Reader) error {
	bufPtr, ok := copyBufferPool.Get().(*copyBuffer)
	if !ok {
		panic("files: copyBufferPool returned unexpected type")
	}
	defer copyBufferPool.Put(bufPtr)

	_, err := io.CopyBuffer(dst, src, bufPtr[:])

	return err
}

// copyFallback copies a single file or a root-level symbolic link from src to dst.
// Permissions and executable bits are preserved.
func copyFallback(src, dst string, srcInfo os.FileInfo) (err error) {
	// Handle symlink at the root level
	if srcInfo.Mode()&os.ModeSymlink != 0 {
		var link string

		link, err = os.Readlink(src)
		if err != nil {
			return fmt.Errorf("copy %s to %s: read symlink %s: %w", src, dst, src, err)
		}

		err = os.Symlink(link, dst)
		if err != nil {
			return fmt.Errorf("copy %s to %s: symlink %s to %s: %w", src, dst, link, dst, err)
		}

		return nil
	}

	// Handle regular file
	srcFile, err := os.Open(src) // #nosec G304
	if err != nil {
		return fmt.Errorf("copy %s to %s: open %s: %w", src, dst, src, err)
	}
	defer func() {
		closeErr := srcFile.Close()
		if closeErr != nil {
			err = errors.Join(err, fmt.Errorf("copy %s to %s: close source file: %w", src, dst, closeErr))
		}
	}()

	// Open or create the destination file with the same permissions
	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode().Perm()) // #nosec G304
	if err != nil {
		return fmt.Errorf("copy %s to %s: open file %s: %w", src, dst, dst, err)
	}
	defer func() {
		closeErr := dstFile.Close()
		if closeErr != nil {
			err = errors.Join(err, fmt.Errorf("copy %s to %s: close destination file: %w", src, dst, closeErr))
		}
	}()

	return copyStream(dstFile, srcFile)
}

// Copy copies a file or directory from src to dst.
// It tries native Copy-on-Write cloning first, then falls back to hard linking,
// and finally to recursive copying.
func Copy(ctx context.Context, src, dst string) error {
	span := trace.SpanFromContext(ctx)

	srcInfo, err := os.Lstat(src)
	if err != nil {
		return err
	}

	// Check if src and dst are the same file/directory
	dstInfo, err := os.Lstat(dst)
	if err == nil && os.SameFile(srcInfo, dstInfo) {
		return nil
	}

	err = os.RemoveAll(dst)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	if srcInfo.IsDir() {
		err = copyOnWriteDir(src, dst)
		if err == nil {
			span.SetAttributes(semconv.FileCopyMethodCopyOnWrite)
			return nil
		}

		c := &copier{allowCoW: true, allowLink: true}

		return c.copyDir(src, dst, srcInfo.Mode())
	}

	err = copyOnWriteFile(src, dst)
	if err == nil {
		span.SetAttributes(semconv.FileCopyMethodCopyOnWrite)
		return nil
	}

	if srcInfo.Mode().IsRegular() {
		err = os.Link(src, dst)
		if err == nil {
			span.SetAttributes(semconv.FileCopyMethodHardlink)
			return nil
		}
	}

	err = copyFallback(src, dst, srcInfo)
	if err == nil {
		span.SetAttributes(semconv.FileCopyMethodCopy)
	}

	return err
}

type copier struct {
	// allowCoW indicates whether copy-on-write cloning is currently supported and should be attempted.
	allowCoW bool
	// allowLink indicates whether hard-linking is currently supported and should be attempted.
	allowLink bool
}

func (c *copier) copyDir(src, dst string, mode os.FileMode) (err error) {
	// Create destination directory with owner-write permissions to ensure files/dirs
	// can be copied into it, even if the source directory is read-only.
	err = os.MkdirAll(dst, mode|0o700)
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

	err = c.copyRoot(srcRoot, dstRoot)
	if err != nil {
		return err
	}

	// Restore original permissions
	err = dstRoot.Chmod(".", mode)
	if err != nil {
		return fmt.Errorf("chmod %s: %w", dst, err)
	}

	return nil
}

func (c *copier) copyRoot(srcRoot, dstRoot *os.Root) (err error) {
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

			err = dstRoot.Symlink(link, name)
			if err != nil {
				return fmt.Errorf("symlink %s to %s: %w", link, name, err)
			}

			continue
		}

		// Handle directory recursively
		if info.IsDir() {
			err = c.copySubDirRoot(srcRoot, dstRoot, name, info.Mode())
			if err != nil {
				return err
			}

			continue
		}

		// Handle regular file
		err = c.copyFileRoot(srcRoot, dstRoot, name, info)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *copier) copySubDirRoot(srcRoot, dstRoot *os.Root, name string, mode os.FileMode) (err error) {
	// Create destination subdirectory with owner-write permissions
	err = dstRoot.Mkdir(name, mode.Perm()|0o700)
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

	err = c.copyRoot(subSrcRoot, subDstRoot)
	if err != nil {
		return err
	}

	// Restore original permissions
	err = subDstRoot.Chmod(".", mode.Perm())
	if err != nil {
		return fmt.Errorf("chmod sub-destination %s: %w", name, err)
	}

	return nil
}

func (c *copier) copyFileRoot(srcRoot, dstRoot *os.Root, name string, info os.FileInfo) (err error) {
	// Try copy-on-write first
	if c.allowCoW {
		srcPath := filepath.Join(srcRoot.Name(), name)
		dstPath := filepath.Join(dstRoot.Name(), name)

		err = copyOnWriteFile(srcPath, dstPath)
		if err == nil {
			return nil
		}

		c.allowCoW = false
	}

	// Try hard link second
	if c.allowLink && info.Mode().IsRegular() {
		srcPath := filepath.Join(srcRoot.Name(), name)
		dstPath := filepath.Join(dstRoot.Name(), name)

		err = os.Link(srcPath, dstPath)
		if err == nil {
			return nil
		}

		c.allowLink = false
	}

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

	return copyStream(dstFile, srcFile)
}
