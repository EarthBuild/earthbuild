package guest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// removeCreated takes away the mount points this engine made, deepest first.
//
// **A mount is a hole.** What was under it stays as it was, and what the step
// wrote into it is not part of what the step produced - so a directory made
// only so there was something to bind onto is this engine's, not the step's,
// and leaving it behind put an empty `/cache` in the image where the reference
// engine puts nothing at all (E33).
//
// Only when empty. `os.Remove` refusing a non-empty directory is the guard
// rather than an inconvenience: a mount point the image already had keeps
// whatever the image put in it.
//
// Deepest first, because a directory containing another cannot go first. That
// is the order the sequential version got from walking the list backwards -
// mounts are applied outermost-first, so reverse order was deepest-first by
// construction rather than by intent, which is why it is stated here instead.
//
// **Removing them concurrently was tried and is slightly worse.** Each removal
// costs about 1.4ms against 0.055ms for the unmount above it, which reads as a
// round trip to the host share and so as something that ought to pipeline.
// Grouping by depth and removing each group at once is safe - two paths at the
// same depth are never nested - and over eight alternating runs came to 7.87ms
// a step against 6.97ms sequential, never once reaching the sequential
// version's fast runs.
//
// The likely reason is that these directories share a parent, so every `rmdir`
// takes that parent's inode lock and the kernel serialises them however they
// are issued; the goroutines are then pure overhead. Latency that does not
// pipeline is not latency that can be hidden.
//
// Recorded because a single run of each said the opposite twice over: the same
// binary measured four times spans 5.67ms to 7.27ms, which is wider than any
// difference here, so one sample of this decides nothing.
func removeCreated(dirs []string) {
	byDepth := map[int][]string{}

	for _, d := range dirs {
		n := depthOf(d)
		byDepth[n] = append(byDepth[n], d)
	}

	depths := make([]int, 0, len(byDepth))
	for n := range byDepth {
		depths = append(depths, n)
	}

	slices.Sort(depths)
	slices.Reverse(depths)

	for _, n := range depths {
		for _, d := range byDepth[n] {
			_ = os.Remove(d)
		}
	}
}

// depthOf is how deep a path is, which is what orders the removals.
//
// Counted on the cleaned path so that `/a/b/` and `/a//b` are the one depth.
// Only ever compared against another path from the same list, so what matters
// is that it is consistent rather than that it is absolute.
func depthOf(p string) int {
	return strings.Count(filepath.Clean(p), string(filepath.Separator))
}
