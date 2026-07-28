//go:build !darwin && !linux && !windows

package files

import "errors"

func copyOnWriteFile(_, _ string) error {
	return errors.New("copy-on-write file cloning not supported on this platform")
}

func copyOnWriteDir(_, _ string) error {
	return errors.New("copy-on-write directory cloning not supported on this platform")
}
