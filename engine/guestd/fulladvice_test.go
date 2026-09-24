package guestd

import (
	"strings"
	"testing"
)

// A guest whose store is a device is not told to run a command that cannot
// reach it.
//
// **`earth prune` collects the host's store directory.** That is the right
// advice for a sandbox sharing this machine's filesystem, where the guest's
// store and the host's are one directory. A microVM's store is a fixed-size
// image the guest has mounted and the host has never opened, so the same
// sentence sends the reader to a command that will collect something else
// entirely and report success.
//
// Written two commits after the message was added, having sent myself there
// first.
func TestTheAdviceMatchesWhereTheStoreIs(t *testing.T) {
	t.Parallel()

	shared := adviceFor("/var/lib/earthbuild")
	if !strings.Contains(shared, "earth prune") {
		t.Errorf("a shared store was not offered the command that collects it: %s", shared)
	}

	device := adviceFor("/store")
	if strings.Contains(device, "earth prune") {
		t.Errorf("a store the host cannot open was offered a host command: %s", device)
	}

	if !strings.Contains(device, "EARTH_VM_STORE") {
		t.Errorf("a full device does not name the setting that sizes it: %s", device)
	}
}
