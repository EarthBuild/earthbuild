//go:build unix

package layer

import "golang.org/x/sys/unix"

// mkdev is the device number a major and a minor make on this platform.
//
// **Not `major<<8 | minor`**, which is how it was spelt out by hand and is how
// no platform here encodes one: Linux packs the high bits of both fields
// elsewhere in a 64-bit word, and macOS uses `major<<24 | minor`. The unpacker
// calls `unix.Mkdev` and the walk reads back what the kernel then reports, so an
// archive reader that invented its own encoding produced a layer that read one
// way from its blob and another from its tree.
//
// Behind a build tag because `unix.Mkdev` is; the *rule* that uses it is not,
// which is the distinction `assembledBy` had to be moved for (E581, E665).
func mkdev(major, minor uint32) uint64 { return unix.Mkdev(major, minor) }
