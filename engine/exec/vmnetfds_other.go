//go:build !linux

package exec

import (
	"fmt"
	"os"
)

// NetFDCommand and NetFDMain exist here so the CLI's dispatch compiles
// everywhere, for the reason NetShimCommand does: `main` decides what its
// arguments mean before it knows anything else, and a build tag in the middle
// of that decision would put a `_linux` file inside the one function every port
// has to read.
//
// What it names is linux to its bones - a tap in a network namespace, and a
// packet socket handed across it - so here it refuses.
const NetFDCommand = "vm-net-fds"

// NetFDMain refuses, because there is no microVM on this platform whose tap
// anybody could be asking for.
func NetFDMain([]string) {
	fmt.Fprintf(os.Stderr, "earth %s: microVMs are a Linux sandbox, and this is not Linux\n",
		NetFDCommand)
	os.Exit(1)
}
