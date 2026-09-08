//go:build !linux

package guest

// RunStepNetShimIfAsked does nothing off Linux: a step's own network namespace
// is a Linux facility, and the microVM backend that needs one runs nowhere else.
func RunStepNetShimIfAsked() {}
