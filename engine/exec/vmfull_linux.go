//go:build linux

package exec

import (
	"errors"
	"fmt"
	"syscall"
)

// EnvVMStore names the block device a microVM's guest keeps its layers on.
//
// A name for what was a bare string in two places, because a failure has to be
// able to tell a reader which setting to change.
const EnvVMStore = "EARTH_VM_STORE"

// vmFullHint explains an ENOSPC that a guest's store device caused.
//
// **The general advice is wrong for a device.** `store.FullHint` ends with
// "deleting the store reclaims all of it", which is the remedy on a host
// filesystem: there, the store is a directory on a disk somebody else sized. A
// guest's store is an image made once at a fixed size, so a build that filled it
// needs a larger one, and the number to change is in the environment rather than
// on the disk.
//
// Empty for anything that is not a full device, because advice about disk space
// attached to a permissions failure is worse than no advice - it sends the
// reader to the wrong place with confidence.
func vmFullHint(err error, storeImage string) string {
	if storeImage == "" || !errors.Is(err, syscall.ENOSPC) {
		return ""
	}

	return fmt.Sprintf("\n  the guest's store device is full: %s is a fixed-size image,"+
		"\n  so this needs a larger one rather than more room on the host"+
		"\n    truncate -s 128G %s && mkfs.xfs -m reflink=1,crc=1 -i nrext64=0 -f %s"+
		"\n  %s names it, and remaking it discards every layer in it: the next"+
		"\n  build is a cold one",
		storeImage, storeImage, storeImage, EnvVMStore)
}
