//go:build !darwin

package exec

// cloneOneFile has no copy-on-write to ask for where the platform offers none.
// The caller copies, which is what every platform did before this existed.
func cloneOneFile(string, string) bool { return false }

// mayClone is false where there is nothing to clone with.
func mayClone() bool { return false }
