//go:build !linux

package guest

// relieveDentries has nothing to release where there is no dentry cache to drop
// and no host serving the lookups.
func relieveDentries() {}
