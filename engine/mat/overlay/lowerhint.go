package overlay

import (
	"fmt"
	"os"
	"strings"
)

// maxMountOptions is what mount(2) will read.
//
// The kernel copies a mount's option string into a single page and stops there
// - `copy_mount_options`, PAGE_SIZE-1 usable - so overlayfs never sees a longer
// one. The failure is not a complaint about length: the string is *truncated*,
// which usually cuts a path in half, and the kernel reports the resulting
// nonexistent directory as ENOENT. A stack whose paths are long enough
// therefore fails with "no such file or directory" naming nothing, at a layer
// count that has nothing to do with overlayfs's own 500-layer wall (MaxStackDepth).
const maxMountOptions = 4095

// lowerHint explains a mount that failed with ENOENT, which the kernel reports
// without saying which path it could not find.
//
// Two causes, and they are told apart by measuring rather than guessed at: a
// lowerdir that genuinely is not there, and an option string too long to arrive
// whole. Returns the empty string when neither applies, so a caller can append
// it unconditionally.
//
// `exists` is a parameter so this can be tested off Linux, where the mount it
// diagnoses cannot run at all. The logic is arithmetic and string handling; the
// only thing platform-specific about it is the failure it explains.
func lowerHint(opts string, lower []string, exists func(string) bool) string {
	if len(opts) > maxMountOptions {
		return fmt.Sprintf(
			"\n  the mount options are %d bytes and the kernel reads at most %d, so the last"+
				"\n  directory in the list arrived cut in half and could not be found"+
				"\n  %d layers at roughly %d bytes each; the limit here is the length of the"+
				"\n  paths, not overlayfs's own limit on how many layers it will stack",
			len(opts), maxMountOptions, len(lower), averageLen(lower))
	}

	for i, dir := range lower {
		if !exists(dir) {
			return fmt.Sprintf(
				"\n  lower layer %d of %d is not there: %s"+
					"\n  a layer named in a stack but absent from the store is a build referring to"+
					"\n  a step whose result was never written - a step that was refused, or one"+
					"\n  whose output nothing captured",
				i+1, len(lower), dir)
		}
	}

	return ""
}

// averageLen is the mean length of the paths, for a message that says where the
// budget went.
func averageLen(paths []string) int {
	if len(paths) == 0 {
		return 0
	}

	total := 0
	for _, p := range paths {
		total += len(p) + 1 // the separator each one carries
	}

	return total / len(paths)
}

// dirExists is the production `exists`.
func dirExists(p string) bool {
	fi, err := os.Stat(p)

	return err == nil && fi.IsDir()
}

// mountOptions is the option string overlayfs is given.
//
// One place, because the length of it is now load-bearing: a diagnostic that
// measured a string the mount did not use would be a confident lie.
//
// `userxattr` moves overlayfs's own metadata from `trusted.overlay.*`, which
// needs CAP_SYS_ADMIN in the *initial* namespace and so is unwritable to a
// rootless build, into `user.overlay.*`, which is not. Without it an
// unprivileged overlay cannot rename a directory out of a lower layer: it tries
// to record a redirect it may not write and returns EIO, which `dpkg` reports as
// `Invalid cross-device link` for any package owning a directory.
func mountOptions(lower []string, upper, work string, userxattr bool) string {
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s",
		strings.Join(lower, ":"), upper, work)

	if userxattr {
		opts += ",userxattr"
	}

	return opts
}

// opaqueXattr is the attribute the mount above will read as "this directory
// replaces the one below".
//
// The other half of the same decision, and it must be taken with it: a marker
// in the namespace the mount is not reading is an attribute the kernel ignores,
// so the directory merges instead of replacing and deleted files reappear -
// with no error anywhere. Paired by `TestTheOpaqueMarkerMatchesTheMount`.
func opaqueXattr(userxattr bool) string {
	if userxattr {
		return "user.overlay.opaque"
	}

	return "trusted.overlay.opaque"
}
