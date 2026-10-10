package guest

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/EarthBuild/earthbuild/engine/fstime"
)

// clampTree gives every entry of a tree the same modification time.
//
// **What a step wrote is the last thing in a build still carrying wall-clock
// time.** Images place reproducibly (E545), unpack reproducibly (E546) and the
// sandbox's own plumbing is out of the delta (E547, E548) - so two machines
// agree about every layer except the ones a step actually produced, whose files
// are stamped with the moment the command ran.
//
// That is the right default and stays it: a build handing its output to `make`
// or to an incremental compiler wants true times, and pinning them would tell
// that compiler nothing had changed. This runs only when the build has asked,
// under the name the rest of the world uses.
//
// Applied to the delta before it is digested, so the identity the layer gets is
// the identity any other machine computes for the same work.
//
// Deepest first, because stamping an entry changes its parent's modification
// time: a directory done before its children would be re-dated by them and
// would carry the clock the clamp exists to remove.
func clampTree(root string, at time.Time) error {
	var paths []string

	err := filepath.WalkDir(root, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		paths = append(paths, p)

		return nil
	})
	if err != nil {
		return fmt.Errorf("read %s to clamp its timestamps: %w", root, err)
	}

	sort.Slice(paths, func(i, j int) bool {
		return strings.Count(paths[i], string(os.PathSeparator)) >
			strings.Count(paths[j], string(os.PathSeparator))
	})

	for _, p := range paths {
		// Without following: a symlink has a time of its own and its target is
		// a second entry this walk will reach on its own account. Stamping
		// through the link would set one entry twice and leave the other with
		// the clock still in it.
		err = fstime.Lchtimes(p, at, at)
		if err != nil {
			return fmt.Errorf("clamp the timestamp of %s: %w", p, err)
		}
	}

	return nil
}
