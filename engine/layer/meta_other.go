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

// observedOwner is the same seam the unix side has, so the test helper that
// installs it compiles everywhere.
//
// Nothing here calls it: `platformMeta` above reads no ownership, because this
// platform reports none (A2). It exists so `layer.go` - which is not
// platform-specific - can name it, which is how a windows build found this: the
// engine had never actually been cross-compiled, so no file had ever been asked
// whether it belonged on the other side of a build tag (E581).
var observedOwner = func(uid, gid uint32) (uint32, uint32) { return uid, gid }
