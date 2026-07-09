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

	size := srcInfo.Size()

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

	err = dstFile.Truncate(size)
	if err != nil {
		err = errorf("truncate dst: %w", err)
		removeErr := os.Remove(dst)
		if removeErr != nil {
			return errors.Join(err, errorf("remove dst: %w", removeErr))
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
			err = errorf("DeviceIoControl: %w", err)
			removeErr := os.Remove(dst)
			if removeErr != nil {
				return errors.Join(err, errorf("remove dst: %w", removeErr))
			}

			return err
		}
	}

	return nil
}
