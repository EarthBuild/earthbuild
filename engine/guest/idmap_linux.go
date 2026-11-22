//go:build linux

package guest

import (
	"os"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

var (
	mapOnce  sync.Once
	uidsSeen layer.IDMap
	gidsSeen layer.IDMap
)

// OwnIDMaps is how this process's ids are named outside its namespace.
//
// Exported because a test on the other side of the guest/host boundary needs to
// do exactly what the guest does, and re-implementing it there would be a
// second copy of the rule - which is the failure this branch has spent a
// fortnight removing.
//
// Read once: a process's own mapping cannot change after it starts, and reading
// `/proc/self/uid_map` per observed path would be a syscall per component of
// every destination.
//
// An unreadable map is the identity, which is the honest answer for a guest
// running as root or on a kernel without namespaces: it sees what the store
// holds, and translating would be the error rather than the fix.
//
// **Both files, because they are two mappings.** The engine writes a uid map
// and a gid map (E105) and they differ whenever a user's group id is not its
// user id - which on the measured machine is always: uid 1000, gid 100. The
// first version of this read `uid_map` and used it for both, turning a gid of 0
// into 1000 instead of 100, and the digests disagreed by exactly the amount
// that looks like nothing (E133).
func OwnIDMaps() (uids, gids layer.IDMap) {
	mapOnce.Do(func() {
		uidsSeen = readIDMap("/proc/self/uid_map")
		gidsSeen = readIDMap("/proc/self/gid_map")
	})

	return uidsSeen, gidsSeen
}

// readIDMap reads one of the kernel's mapping files, or the identity.
func readIDMap(path string) layer.IDMap {
	f, err := os.Open(path) //nolint:gosec // a fixed procfs path
	if err != nil {
		return layer.IDMap{}
	}

	defer f.Close()

	m, err := layer.ParseIDMap(f)
	if err != nil {
		return layer.IDMap{}
	}

	return m
}
