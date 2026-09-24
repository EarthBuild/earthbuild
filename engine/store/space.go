package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// sizeBudget is how long Size spends before answering with what it has.
//
// It runs on a path that has already failed, where a slow answer is another
// thing going wrong. A floor reported as a floor is worth more than an exact
// figure nobody waits for (I11).
// A var rather than a const so a test can make the budget bite. Growing a tree
// until a real 300ms runs out produces a test that passes on a slow machine and
// skips on a fast one, which is a test that reports the machine rather than the
// code.
var sizeBudget = 300 * time.Millisecond

// Free reports the bytes available to this user on the filesystem holding path.
//
// Available rather than free: the reserved blocks a filesystem keeps for root
// are not space a build can have, and reporting them is how a diagnostic ends up
// insisting there is room while the write keeps failing.
func Free(path string) (uint64, error) { return freeOn(path) }

// Size reports how much of the disk the store is using, and whether that figure
// is the whole of it.
//
// False means the walk ran out of budget and the number is a floor. A store
// large enough to be the problem is also large enough to be slow to measure,
// so the two answers arrive together rather than the caller choosing.
func Size(root string) (uint64, bool) { return sizeWithin(root, time.Now().Add(sizeBudget)) }

// SizeAll measures without a budget, for a caller that has to be right.
//
// **A floor is a fine answer for a diagnostic and a wrong one for a decision.**
// The collector sized layers through Size, took the floor for the total, decided
// the store already fitted and removed nothing - reporting 2.3 GiB of a store
// holding 15 GiB. A mechanism that is switched off and one that found nothing
// produce the same output, which is this project's most recorded failure, and it
// arrived here through a discarded second return value (E574).
func SizeAll(root string) uint64 {
	n, _ := sizeWithin(root, time.Time{})

	return n
}

// sizeWithin walks root, stopping at deadline. A zero deadline is no deadline.
func sizeWithin(root string, deadline time.Time) (uint64, bool) {
	if root == "" {
		return 0, false
	}

	var (
		total    uint64
		complete = true
	)

	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // a store being measured while it changes
		}

		if !deadline.IsZero() && time.Now().After(deadline) {
			complete = false

			return filepath.SkipAll
		}

		fi, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // as above
		}

		total += occupies(fi)

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

// apparent is what a file holds, guarded against a negative size.
func apparent(fi os.FileInfo) uint64 {
	if fi.Size() < 0 {
		return 0
	}

	return uint64(fi.Size()) //nolint:gosec // guarded non-negative above
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

// ParseSize reads a size a person wrote: a number and a unit, as 4g or 512m.
//
// The same forms overlay's `sizeLooksRight` accepts, and for the same reason it
// refuses percentages: a share of a machine nobody has measured is a setting
// that works everywhere it is tried and fills the machine where it is not. A
// bare number is bytes, because that is what a bare number is everywhere else
// here.
func ParseSize(s string) (uint64, error) {
	if s == "" {
		return 0, errors.New("a size is a number and a unit, as 4g or 512m")
	}

	mult := uint64(1)

	switch s[len(s)-1] {
	case 'k', 'K':
		mult = 1 << 10
	case 'm', 'M':
		mult = 1 << 20
	case 'g', 'G':
		mult = 1 << 30
	case 't', 'T':
		mult = 1 << 40
	}

	digits := s
	if mult > 1 {
		digits = s[:len(s)-1]
	}

	n, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf(
			"%q is not a size: write a number and a unit, as 4g or 512m"+
				"\n  a percentage is not accepted: a share of a machine nobody has"+
				"\n  measured is the setting that fills the machines it was not measured on", s)
	}

	if n > 1<<63/mult {
		return 0, fmt.Errorf("%q is larger than any disk this addresses", s)
	}

	return n * mult, nil
}
