// Command earth-guestd is the sandbox agent, kept as its own binary for the
// arrangements that still expect one.
//
// The agent itself lives in engine/guestd and is reachable as `earth guestd`,
// which is how it travels into a step: a nested build runs a copy of the CLI and
// there is nowhere beside it to put a second file.
package main

import (
	"os"

	"github.com/EarthBuild/earthbuild/engine/guestd"
)

func main() { guestd.Main(os.Args[1:]) }
