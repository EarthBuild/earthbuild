//go:build !linux

package guest

// dropVacuousOpaque does nothing where there is no overlayfs to mark anything.
func dropVacuousOpaque(string, func(string) bool) {}
