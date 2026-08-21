package exec

import "os"

// EnvClone turns on whole-tree cloning when materialising a base.
//
// **Opt-in, and that is a retreat rather than a design.** `clonefile(2)` places
// a 267MB, 17,580-entry base image in 0.26s where hard-linking it takes 8.5s,
// and it is the safer of the two - a hard link makes one file with two names, so
// a write through the layer store reaches into the shared image cache, while a
// clone diverges on first write.
//
// It is off because turning it on made `go mod download` hang. Twice, cold, for
// the whole of a 900-second budget, in a step that had taken seconds all
// evening; the materialise itself was visibly faster - the first `apk` line
// arrived at 6s rather than 19s - and then the build stopped. The cause is not
// established, and a build tool does not default onto a path whose failure its
// author cannot explain. Suspected: the guest mounts the placed tree as an
// overlay lowerdir, and a clone shares extents where a link shares inodes, which
// is a difference the layer machinery is known to care about (E89 records
// `layer.Take` recording inode identity).
const EnvClone = "EARTH_CLONE_TREES"

// placeTree puts a copy of src at dst, by whatever means the filesystem allows.
//
// dst must be a destination nobody else can reach - both callers fill a
// temporary directory and rename the finished tree into place - because the link
// path skips the per-entry staging that defends against a second writer.
func placeTree(src, dst string) error {
	if os.Getenv(EnvClone) != "" {
		if err := cloneTree(src, dst); err == nil {
			return nil
		}
		// Falling through is not an error path: a separated image cache is often
		// on another volume, and every filesystem that is not APFS takes it.
	}

	return linkTreeExclusive(src, dst)
}
