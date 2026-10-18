//go:build linux

package guest

// cannotRunDaemon says why a daemon cannot be run here, or nothing.
//
// Linux can, in principle: a step is already inside namespaces this engine made,
// and a dockerd starts in a plain user namespace with no rootlesskit and no
// slirp4netns (E364). Whether *this* machine has the pieces is the host's
// question and is asked before the step is sent (E363), not again here - a
// second answer is a second place for the two to disagree.
func cannotRunDaemon() string { return "" }
