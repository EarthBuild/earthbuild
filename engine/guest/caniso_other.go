//go:build !linux

package guest

// CanIsolate has nothing to refuse off Linux, where a step takes the
// unconfined path and needs no privilege to do it.
func CanIsolate() error { return nil }
