package cli_test

import "runtime"

// runtimeArch is the architecture the Earthfile files artifacts under, which is
// Go's name for it.
func runtimeArch() string { return runtime.GOARCH }
