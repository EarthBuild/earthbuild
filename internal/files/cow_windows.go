//go:build windows

package files

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

type duplicateExtentsData struct {
	FileHandle       windows.Handle
	SourceFileOffset int64
	TargetFileOffset int64
	ByteCount        int64
}

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

	size := srcInfo.Size()

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

	err = dstFile.Truncate(size)
	if err != nil {
		err = fmt.Errorf("copy on write file %q to %q: truncate dst: %w", src, dst, err)
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

	if size == 0 {
		return nil
	}

	srcHandle := windows.Handle(srcFile.Fd())
	dstHandle := windows.Handle(dstFile.Fd())

	// FSCTL_DUPLICATE_EXTENTS_TO_FILE has a limit of 4GB per call.
	var chunkSize int64 = 4 * 1024 * 1024 * 1024 // 4GB
	for offset := int64(0); offset < size; offset += chunkSize {
		length := chunkSize
		if offset+length > size {
			length = size - offset
		}

		data := duplicateExtentsData{
			FileHandle:       srcHandle,
			SourceFileOffset: offset,
			TargetFileOffset: offset,
			ByteCount:        length,
		}

		var bytesReturned uint32
		err = windows.DeviceIoControl(
			dstHandle,
			windows.FSCTL_DUPLICATE_EXTENTS_TO_FILE,
			(*byte)(unsafe.Pointer(&data)),
			uint32(unsafe.Sizeof(data)),
			nil,
			0,
			&bytesReturned,
			nil,
		)
		if err != nil {
			err = fmt.Errorf("copy on write file %q to %q: DeviceIoControl: %w", src, dst, err)
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
	}

	return nil
}

func copyOnWriteDir(_, _ string) error {
	return errors.New("copy-on-write directory cloning not supported on Windows")
}
