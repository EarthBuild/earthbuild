//go:build unix

package guest

import (
	"os"
	"syscall"
)

// ownerOf reads a file's uid and gid.
//
// Behind a build tag because `syscall.Stat_t` is not portable, and the `ok`
// return is not decoration: a platform that cannot report ownership must make
// `--keep-own` fail rather than silently copy the running user's. See the
// other file for the case where there is no answer.
func ownerOf(fi os.FileInfo) (uid, gid int, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}

	return int(st.Uid), int(st.Gid), true
}
