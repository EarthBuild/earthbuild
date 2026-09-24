package layer

import "strings"

// **Not behind a build tag**, though it lived behind one until an archive
// reader needed it. The rule is about attribute *names* and has nothing
// platform-specific in it; the reader that applies it does, and putting the two
// together made a windows build fail on a file that had no business being
// unbuildable there - which is E581's lesson, arriving a second time.
//
// assembledBy reports whether an attribute describes how a filesystem was put
// together rather than what is at a path.
//
// overlayfs keeps its bookkeeping in extended attributes and writes it onto the
// *upper* layer - `user.overlay.origin` records which lower inode an entry was
// copied up from, under `userxattr`; `trusted.overlay.*` is the same thing on a
// privileged mount. This engine commits that upper layer, stores it, and hashes
// it, so without this a directory a step copied into carries a fingerprint of
// the layers underneath it:
//
//	the same copy over two base images produces two layer digests
//	an observation of that directory goes stale whenever the base moves
//
// Green paper §3.3 lists what a layer records, and "which lower inode did this
// come from" is not on the list. It is a property of an assembly, not of a
// file (E132).
//
// Narrow on purpose. Dropping every extended attribute would be the mirror
// mistake and would lose a `setcap` grant on the way into an image, which is
// the defect E92 existed to fix.
// `com.apple.` is the same argument from the other direction, and the guest's
// copy already excludes it (`ours`): macOS stamps files with attributes of its
// own - `com.apple.provenance` appears on a file this engine's own tests just
// wrote - and hashing them makes a layer's identity depend on which machine
// captured it. The rule was in one of the two places that hash a tree.
func assembledBy(name string) bool {
	return strings.HasPrefix(name, "user.overlay.") ||
		strings.HasPrefix(name, "trusted.overlay.") ||
		strings.HasPrefix(name, "com.apple.")
}
