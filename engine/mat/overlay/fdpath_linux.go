package overlay

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// byDescriptor names each lower layer through /proc/self/fd, and returns a
// closer.
//
// The symlink farm shortens a layer's *name* to twelve characters; it cannot
// shorten the path to the farm, so how many layers fit in the kernel's one page
// of mount options depends on how deep the store happens to live. A store under
// a home directory fits about eighty layers and one under a long temporary path
// fails at thirty-seven - the same stack, the same kernel, a different answer
// because of where somebody put a directory.
//
// `/proc/self/fd/<n>` is at most eighteen bytes and does not vary with the
// store at all. The kernel resolves it like any other path, which is checked
// rather than assumed: a probe mounted two lowers this way and read a file back
// through the result (E163).
//
// The descriptors must outlive the mount call and no longer: overlayfs resolves
// the path while mounting and keeps nothing, so the closer runs immediately
// afterwards. O_PATH because nothing here reads the directory - it is a name,
// not a handle - and O_PATH descriptors cost the least and permit the least.
//
// Falls back to the paths it was given, layer by layer, for the reason `link`
// does: a mount that would have worked with long paths must not fail because a
// shortening did not apply. `tooLong` still measures what was actually built,
// and `all` tells it whether every layer was shortened, so a refusal can name
// the cause rather than blaming the stack (I11, E188).
func byDescriptor(lower []string) (out []string, closeAll func(), all bool) {
	fds := make([]int, 0, len(lower))

	closeAll = func() {
		for _, fd := range fds {
			_ = unix.Close(fd)
		}
	}

	out = make([]string, 0, len(lower))

	all = true

	for _, dir := range lower {
		fd, err := unix.Open(dir, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			// This one keeps its own path, and the caller is told that not every
			// layer was shortened: a refusal that blames the stack while one
			// layer quietly cost ninety bytes is a degradation reported without
			// its cause, which is what I11 forbids.
			out = append(out, dir)
			all = false

			continue
		}

		fds = append(fds, fd)
		out = append(out, fmt.Sprintf("/proc/self/fd/%d", fd))
	}

	return out, closeAll, all
}

// procIsMounted reports whether /proc/self/fd is usable for naming paths.
//
// Asked rather than assumed: the guest mounts /proc for a step, but a caller
// embedding this materialiser need not have one, and a lowerdir naming a path
// that is not there fails as ENOENT - the kernel's least informative answer,
// and the exact symptom this shortening exists to avoid.
func procIsMounted() bool {
	_, err := os.Lstat("/proc/self/fd")

	return err == nil
}
