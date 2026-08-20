//go:build !linux

package guest

import (
	"fmt"
	"os"
)

// copySpecial has no answer off Linux, and says so.
//
// A delta is only ever committed inside the sandbox, which is Linux; this file
// exists so the package builds on the machine the tests run on. Refusing rather
// than skipping is the point of the change it belongs to: an entry that cannot
// be reproduced must not be dropped in silence.
func copySpecial(_, _, name string, fi os.FileInfo) (bool, error) {
	return false, fmt.Errorf("cannot reproduce %s (%s) on this platform", name, fi.Mode().Type())
}
