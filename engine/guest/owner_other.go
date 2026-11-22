//go:build !unix

package guest

import "os"

// ownerOf has no answer here, and says so.
//
// `--keep-own` then refuses rather than copying whatever the running process
// happens to own, which would be an approximation presented as the feature
// (green paper I10).
func ownerOf(os.FileInfo) (uid, gid int, ok bool) { return 0, 0, false }
