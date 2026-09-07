//go:build !linux

package guest

// RunStepShimIfAsked does nothing where there are no namespaces to enter.
//
// A no-op rather than a refusal: the callers are `main` functions that must go
// on to do their real work, and the launch that would ask for a shim cannot
// happen off Linux either - `isolate` refuses first.
func RunStepShimIfAsked() {}
