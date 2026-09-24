//go:build !linux

package exec

// guestSettings is the Linux backend's; elsewhere there is no list to read and
// the source guard beside this is what holds the property.
func guestSettingsForTest() []string { return nil }
