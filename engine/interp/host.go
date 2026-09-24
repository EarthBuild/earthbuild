package interp

import (
	"fmt"
	"net"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

// hostEntry reads `HOST <name> <address>` and refuses anything else.
//
// **Checked here rather than written through.** These two words become a line in
// the step's hosts file, and a wrong one does not fail: a name with no address
// resolves to nothing, and an address that is not one resolves to whatever the
// resolver makes of the text. Either is a build fetching from somewhere nobody
// chose, reported as a network error at best.
//
// The address is parsed rather than pattern-matched, because `10.0.0.256` looks
// like an address and is not one.
func hostEntry(c earthfile.Command) (string, error) {
	switch {
	case len(c.Args) < 2:
		return "", fmt.Errorf("HOST needs a hostname and an address (%s)",
			loc(c.SourceLocation))

	case len(c.Args) > 2:
		return "", fmt.Errorf("HOST takes a hostname and an address, and %q is a"+
			" third argument (%s)", c.Args[2], loc(c.SourceLocation))
	}

	if net.ParseIP(c.Args[1]) == nil {
		return "", fmt.Errorf("HOST %s: %q is not an IP address (%s)",
			c.Args[0], c.Args[1], loc(c.SourceLocation))
	}

	return c.Args[0] + " " + c.Args[1], nil
}
