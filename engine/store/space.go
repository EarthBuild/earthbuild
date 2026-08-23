package store

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// sizeBudget is how long Size spends before answering with what it has.
//
// It runs on a path that has already failed, where a slow answer is another
// thing going wrong. A floor reported as a floor is worth more than an exact
// figure nobody waits for (I11).
const sizeBudget = 300 * time.Millisecond

// Free reports the bytes available to this user on the filesystem holding path.
//
// Available rather than free: the reserved blocks a filesystem keeps for root
// are not space a build can have, and reporting them is how a diagnostic ends up
// insisting there is room while the write keeps failing.
func Free(path string) (uint64, error) {
	var st unix.Statfs_t

	err := unix.Statfs(path, &st)
	if err != nil {
		return 0, fmt.Errorf("ask the filesystem at %s how much is left: %w", path, err)
	}

	// Both fields are small non-negative quantities whose types differ by
	// platform, which is the one thing this conversion is for.
	return uint64(st.Bavail) * uint64(st.Bsize), nil //nolint:gosec,unconvert // see above
}

// Size reports how much of the disk the store is using, and whether that figure
// is the whole of it.
//
// False means the walk ran out of budget and the number is a floor. A store
// large enough to be the problem is also large enough to be slow to measure,
// so the two answers arrive together rather than the caller choosing.
func Size(root string) (uint64, bool) {
	if root == "" {
		return 0, false
	}

	var (
		total    uint64
		complete = true
		deadline = time.Now().Add(sizeBudget)
	)

	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // a store being measured while it changes
		}

		if time.Now().After(deadline) {
			complete = false

			return filepath.SkipAll
		}

		if d.IsDir() {
			return nil
		}

		fi, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // as above
		}

		total += uint64(fi.Size()) //nolint:gosec // a file size is not negative

		return nil
	})
	if err != nil {
		return total, false
	}

	return total, complete
}

// FullHint explains an out-of-space error against the store.
//
// The engine used to have nothing to add to ENOSPC - "there the disk really is
// full", as the scratch tmpfs hint puts it. That was true while the store was
// somebody else's directory. It is not true now: **the store has no collector**,
// so a machine that has run this engine for a while is a machine whose disk this
// engine has been quietly filling, and the store's own size is the first number
// anybody wants (E571).
//
// Empty for every other error, on the rule the other hints follow.
func FullHint(err error, root string) string {
	if !errors.Is(err, syscall.ENOSPC) || root == "" {
		return ""
	}

	size, complete := Size(root)

	about := "holding"
	if !complete {
		about = "holding at least"
	}

	hint := fmt.Sprintf(
		"\n  the disk is full, and the store is %s %s"+
			"\n    %s"+
			"\n  nothing collects it yet: every build that changes a source file files new"+
			"\n  layers and no build removes the old ones, so this grows without limit",
		about, human(size), root)

	free, ferr := Free(root)
	if ferr == nil {
		hint += fmt.Sprintf("\n  %s is left on that filesystem", human(free))
	}

	return hint + "\n  deleting the store reclaims all of it; the next build is a cold one"
}

// human is a size a person can read, in the units a disk is sold in.
func human(n uint64) string {
	const unit = 1024

	if n < unit {
		return fmt.Sprintf("%d B", n)
	}

	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}
