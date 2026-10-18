//go:build darwin

package exec

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// cloneOneFile puts a copy of src at dst without moving its bytes.
//
// **An export is a copy the filesystem can make for nothing.** `copyOut` read a
// 45MB artifact into memory and wrote it back out, twice a second on a build
// that had nothing else to do (E566); APFS shares the extents instead and
// diverges on the first write, which is exactly what a caller of a *copy* is
// entitled to expect and what makes this safe for a file the user then edits.
//
// Staged beside the destination and renamed over it, because `clonefile`
// refuses a destination that exists and `Remove` then clone is a window in
// which the artifact from the last build is gone and this one has not arrived.
//
// Reports whether it cloned. Failure is not an error: a store on another volume,
// a filesystem that is not APFS and a destination on a network mount are all
// ordinary, and the caller's answer to each is the copy it was going to make.
func cloneOneFile(src, dst string) bool {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".cloning-")
	if err != nil {
		return false
	}

	staged := tmp.Name()

	// The name is wanted and the file is not: `clonefile` will not write over
	// one. Created first so the name is this call's, which is what stops two
	// exports of one artifact from racing on it.
	_ = tmp.Close()
	_ = os.Remove(staged)

	err = unix.Clonefile(src, staged, 0)
	if err != nil {
		_ = os.Remove(staged)

		return false
	}

	err = os.Rename(staged, dst)
	if err != nil {
		_ = os.Remove(staged)

		return false
	}

	return true
}

// clonesReported names the environment variable that turns cloning off.
//
// A switch rather than a certainty, for the same reason `EARTH_CLONE_TREES`
// exists: cloning has been wrong once before, for reasons that turned out to be
// about something else entirely (E510), and a build that can be told to copy is
// a build whose next mystery can be bisected in one command.
const clonesReported = "EARTH_CLONE_EXPORTS"

// mayClone reports whether this invocation is allowed to clone an export.
func mayClone() bool {
	v := os.Getenv(clonesReported)

	return v != "0" && v != "false" && v != "no"
}

// cloneNote is what to say when a clone was refused for a reason worth knowing.
func cloneNote(dst string) string {
	return fmt.Sprintf("could not clone %s; copying instead", dst)
}
