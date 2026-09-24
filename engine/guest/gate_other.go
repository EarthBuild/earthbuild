//go:build !linux

package guest

// WaitForIDs has nothing to wait for where there are no user namespaces.
func WaitForIDs() error { return nil }
