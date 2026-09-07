//go:build !linux

package overlay

// tmpfs is Linux-only, like everything else this package mounts.
func tmpfs() (string, func(), error) { return "", nil, ErrUnsupported }
