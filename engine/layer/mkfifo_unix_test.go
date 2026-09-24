//go:build unix

package layer_test

import "golang.org/x/sys/unix"

func mkfifo(p string) error { return unix.Mkfifo(p, 0o600) }
