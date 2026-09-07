//go:build linux

package exec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// cloneOneFile puts a copy of src at dst without the bytes going through this
// process.
//
// **The same saving darwin has had, on the platform that builds.** `copyOut`
// reads a file into memory and writes it back; E566 measured that against a 45MB
// artifact and gave darwin `clonefile`, which leaves Linux - where CI runs -
// doing the read-and-write for every file of every context and every export.
//
// `copy_file_range` is the kernel's answer to the same question. On btrfs and
// XFS it shares extents exactly as APFS does, so the copy is a reflink and costs
// nothing until one side is written; on ext4 it still copies, but in the kernel,
// so a 45MB artifact no longer becomes a 45MB allocation here.
//
// The mode is set here and the times are not, which is what `clonefile` does on
// darwin: `stampOut` runs afterwards in either case and sets times alone. A
// version of this that copied bytes only left every file at 0600, and the
// characterisation test caught it before it ran anywhere.
//
// Staged beside the destination and renamed over it, matching darwin - so a
// destination that already exists is replaced atomically rather than being
// absent for a moment.
//
// Reports whether it copied. Failure is not an error: a source on one filesystem
// and a destination on another, a kernel older than 4.5, and a /proc file whose
// size it cannot know are all ordinary, and the caller's answer to each is the
// copy it was going to make anyway.
func cloneOneFile(src, dst string) bool {
	in, err := os.Open(src)
	if err != nil {
		return false
	}

	defer func() { _ = in.Close() }()

	fi, err := in.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".cloning-")
	if err != nil {
		return false
	}

	staged := tmp.Name()

	if !copyRange(in, tmp, fi.Size()) {
		_ = tmp.Close()
		_ = os.Remove(staged)

		return false
	}

	// **The mode travels with the file, because the caller assumes it did.**
	// `clonefile` on darwin carries mode and times, and `stampOut` afterwards
	// sets only the times - so a clone that copied bytes alone left every file
	// at `CreateTemp`'s 0600. A read-only file arriving writable is the kind of
	// difference a build notices much later and blames on something else.
	err = tmp.Chmod(fi.Mode())
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(staged)

		return false
	}

	err = tmp.Close()
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

// copyRange moves size bytes from in to out with the kernel doing the moving.
//
// Looped because one call is permitted to copy less than it was asked for, and
// a short copy taken for a whole one is a truncated file that nothing reports.
func copyRange(in, out *os.File, size int64) bool {
	for done := int64(0); done < size; {
		n, err := unix.CopyFileRange(int(in.Fd()), nil, int(out.Fd()), nil, int(size-done), 0)
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		case err != nil:
			return false
		case n == 0:
			// Nothing copied and no error means the source ended sooner than
			// its size promised. Reporting success would leave a short file.
			return done == size
		}

		done += int64(n)
	}

	return true
}

// mayClone reports whether this invocation is allowed to clone.
//
// The same switch darwin uses, and for the same reason: a build that can be told
// to copy is a build whose next mystery can be bisected in one command.
const clonesReported = "EARTH_CLONE_EXPORTS"

func mayClone() bool {
	v := os.Getenv(clonesReported)

	return v != "0" && v != "false" && v != "no"
}

// cloneNote is what to say when a clone was refused for a reason worth knowing.
func cloneNote(dst string) string {
	return fmt.Sprintf("could not clone %s; copying instead", dst)
}
