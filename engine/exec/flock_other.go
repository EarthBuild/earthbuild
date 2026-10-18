//go:build windows

package exec

import (
	"errors"
	"os"
)

// tryFlock refuses, because this platform has no flock and no engine either.
//
// **Unreachable rather than unimplemented.** The engine runs steps in Linux
// sandboxes; the windows artifact is the client. Nothing here takes a store
// lock on windows, so a refusal is the honest stub - it compiles the package,
// and if the assumption ever stops holding it says so instead of silently
// letting two builds share a store.
func tryFlock(*os.File) error {
	return errors.New("this platform cannot lock a layer store: the engine runs on Linux")
}
