//go:build !unix

package guest

// copyXattrs has nothing to carry where there are no extended attributes.
func copyXattrs(_, _ string) error { return nil }
