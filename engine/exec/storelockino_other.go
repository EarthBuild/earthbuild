//go:build windows

package exec

// storeInode has no answer on a platform with no inodes.
//
// The caller treats "no inode" as "cannot tell two stores apart by identity"
// and falls back to comparing paths, which is what this platform has.
func storeInode(string) (uint64, bool) { return 0, false }
