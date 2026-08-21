package exec

import "os"

// EnvClone turns whole-tree cloning off.
//
// On by default. `clonefile(2)` places a 267MB, 17,580-entry base image in
// 0.26s where hard-linking each entry takes 8.51s, and it is the safer of the
// two besides: a hard link makes one file with two names, so a write through the
// layer store reaches into the shared image cache, while a clone diverges on the
// first write - which is what a caller of a *copy* is entitled to expect.
//
// **It was off for one increment, on evidence that turned out to be about
// something else.** With cloning on, a build appeared to hang after its step
// completed; with it off, the same build finished. The difference was real and
// the conclusion was wrong. Every one of those runs was made on a machine
// carrying a dozen leaked sandbox VMs, each holding tens of thousands of open
// descriptors on the layer store, and the system-wide limit was the thing being
// hit. Cloning made materialising a base fast enough to reach the file-heavy
// step sooner, which is why it looked causal (E510).
//
// Set it to anything falsy to fall back to hard links: another filesystem, or a
// platform with no directory clone, takes that path anyway.
const EnvClone = "EARTH_CLONE_TREES"

// placeTree puts a copy of src at dst, by whatever means the filesystem allows.
//
// dst must be a destination nobody else can reach - both callers fill a
// temporary directory and rename the finished tree into place - because the link
// path skips the per-entry staging that defends against a second writer.
func placeTree(src, dst string) error {
	if v := os.Getenv(EnvClone); v != "0" && v != "false" && v != "no" {
		if err := cloneTree(src, dst); err == nil {
			return nil
		}
		// Falling through is not an error path: a separated image cache is often
		// on another volume, and every filesystem that is not APFS takes it.
	}

	return linkTreeExclusive(src, dst)
}
