//go:build !unix

package layer

import "io/fs"

// platformMeta has nothing to add where the platform has no uid, gid or inode.
// Assumption A2: results stay correct, but a layer captured here cannot record
// ownership, and the engine must not pretend otherwise.
func platformMeta(*entry, fs.FileInfo, map[uint64]string) {}

func readXattrs(string) ([]xattr, error) { return nil, nil }

// setXattrs has nothing to restore where nothing was captured.
func setXattrs(string, []xattr) error { return nil }
