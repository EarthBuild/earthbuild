package guestd

import (
	"strings"

	"github.com/EarthBuild/earthbuild/cmd/earth-vmboot/vmboot"
)

// adviceFor is what to do about a store this agent could not collect enough of,
// which depends on what the store is.
//
// **`earth prune` collects the host's store directory.** That is right for a
// sandbox sharing this machine's filesystem, where the guest's store and the
// host's are one directory and the command reaches it. A microVM's store is a
// fixed-size image that the guest has mounted and the host has never opened, so
// the same sentence sends a reader to a command that collects something else
// and then reports success - the worst kind of advice, because it appears to
// work.
//
// Decided from the store's own path, which is the only thing here that knows:
// the agent is one binary and does not otherwise care which backend started it.
func adviceFor(root string) string {
	if root == vmboot.StoreAt || strings.HasPrefix(root, vmboot.StoreAt+"/") {
		return "  this store is a fixed-size image and the host cannot collect it:" +
			" a build that\n  runs out of room needs a larger one, named by " +
			vmboot.EnvVMStore + "\n"
	}

	return "  `earth prune` collects it with no budget, when you can spare the wait\n"
}
