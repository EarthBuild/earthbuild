//go:build !unix

package layer

import (
	"io/fs"
	"time"
)

// platformMeta has nothing to add where the platform has no uid, gid or inode.
// Assumption A2: results stay correct, but a layer captured here cannot record
// ownership, and the engine must not pretend otherwise.
func platformMeta(*entry, fs.FileInfo, map[uint64]string) {}

func readXattrs(string) ([]xattr, error) { return nil, nil }

// setXattrs has nothing to restore where nothing was captured.
func setXattrs(string, []xattr) error { return nil }

// Lchtimes cannot stamp a link where the platform has no such call. Left alone
// rather than faked: the caller's digest check disagrees, and a disagreement is
// better than a layer that claims to match.
func Lchtimes(string, time.Time) error { return nil }
