//go:build linux

package exec

import (
	"errors"
	"fmt"
	"strings"
	"syscall"

	"github.com/EarthBuild/earthbuild/cmd/earth-vmboot/vmboot"
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
	if storeImage == "" || !outOfSpace(err) {
		return ""
	}

	return fmt.Sprintf("\n  the guest's store device is full: %s is a fixed-size image,"+
		"\n  so this needs a larger one rather than more room on the host"+
		"\n    truncate -s 128G %s && mkfs.xfs -m reflink=1,crc=1 -i nrext64=0 -f %s"+
		"\n  %s names it, and remaking it discards every layer in it: the next"+
		"\n  build is a cold one",
		storeImage, storeImage, storeImage, EnvVMStore)
}

// storeFull is how a guest says its store filled, once the message has crossed
// the protocol and stopped being an error anybody can unwrap.
//
// Paired with the store's own path before it is believed, so a step whose own
// output quotes the ENOSPC message - a test asserting it, a log being echoed -
// does not match.
const storeFull = "no space left on device"

// outOfSpace reports whether err is this engine's store filling up.
//
// **A wrapped Errno does not survive the wire.** A microVM's ENOSPC always
// happens in the guest, where the store is a device only the guest has mounted,
// and the host learns of it through the protocol - which carries a message, not
// a `syscall.Errno`. `errors.Is` is therefore false for every failure this hint
// was written to explain, so the hint was present and unreachable: a build died
// with "no space left on device" writing a layer, and said nothing about the
// fixed-size image that had filled.
//
// Both checks, not one. The typed check still catches a host-side failure with
// its error intact, and is the exact one where it applies; the text is the only
// thing left after a crossing.
func outOfSpace(err error) bool {
	if errors.Is(err, syscall.ENOSPC) {
		return true
	}

	if err == nil {
		return false
	}

	// Anchored on the store path as well as the message, so a step that merely
	// prints the words is not mistaken for the store it is running on.
	//
	// Both paths, because a guest reaches its store by different routes: a
	// microVM mounts the device at vmboot.StoreAt, and a sandbox sharing this
	// machine's filesystem sees guest.StorePath. Anchoring on the wrong one is
	// how the first attempt at this still matched nothing.
	msg := err.Error()
	if !strings.Contains(msg, storeFull) {
		return false
	}

	return strings.Contains(msg, vmboot.StoreAt+"/") || strings.Contains(msg, guestStore)
}
