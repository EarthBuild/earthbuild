//go:build !linux

package exec

import (
	"fmt"
	"os"
)

// NetShimCommand and NetShimMain exist here so the CLI's dispatch compiles
// everywhere.
//
// **A word, not a build tag, at the call site.** `main` decides what its
// arguments mean before it knows anything else, and threading a platform
// condition through that decision would put a `_linux` file in the middle of
// the one function every port has to read. The shim itself is linux to its
// bones - user namespaces, tap devices, packet sockets - so here it refuses.
const NetShimCommand = "vm-net"

// NetShimMain refuses, because there is no microVM on this platform to give a
// network to.
func NetShimMain([]string) {
	fmt.Fprintf(os.Stderr, "earth %s: microVMs are a Linux sandbox, and this is not Linux\n",
		NetShimCommand)
	os.Exit(1)
}
