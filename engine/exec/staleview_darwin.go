//go:build darwin

package exec

import (
	"os"
	"strconv"
	"syscall"
)

// LabelStoreInode records which directory a sandbox was started against.
//
// **Existence is not identity.** `reapStranded` removes a VM whose mounted
// directories have gone, which is the right rule for a store that was deleted
// and left deleted. A store deleted and *recreated* - which anything opening the
// layer store does, the engine included - leaves the path there and the VM's
// virtiofs mount pointing at the inode that went.
//
// The symptom is not reliably an error. Measured while benchmarking: two builds
// in six went wrong, one producing nothing and one taking 477 seconds for a
// single `RUN echo`, and an earlier run reached step 34 of 45 in ten minutes.
// A hang with no output is the worst thing this engine can do, and it is worth a
// label to turn it into a reboot.
//
// A label rather than a file beside the store, because the store is the thing
// that gets deleted; the backend holds this for exactly as long as the VM it
// describes.
const LabelStoreInode = "earthbuild.store-inode"

// SandboxSeesStore reports whether a running sandbox is looking at the directory
// this build is about to use.
//
// **Reused when the answer is not knowable.** A VM started before this engine
// wrote the label, or one carrying a label that will not parse, is kept:
// refusing every unlabelled sandbox would discard every machine running at the
// moment of an upgrade, and that cost is certain where the fault this prevents
// is occasional.
func SandboxSeesStore(labels map[string]string, now uint64) bool {
	was, ok := labels[LabelStoreInode]
	if !ok {
		return true
	}

	ino, err := strconv.ParseUint(was, 10, 64)
	if err != nil {
		return true
	}

	return ino == now
}

// inodeOf is a directory's identity, or zero where the platform will not say.
//
// Zero is "unknown" and never matches a recorded inode, so a caller that cannot
// stat the store gets the same answer as one whose store has moved - which is
// the safe direction: a sandbox rebooted needlessly costs a boot, and one reused
// wrongly costs a build.
func inodeOf(dir string) uint64 {
	fi, err := os.Stat(dir)
	if err != nil {
		return 0
	}

	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}

	return st.Ino
}
