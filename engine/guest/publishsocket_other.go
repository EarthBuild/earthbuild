//go:build !linux

package guest

import "errors"

// publishSocket cannot bind off Linux, where `cannotRunDaemon` has already
// refused any step that asked for a daemon - so reaching this is a bug rather
// than a platform limit, and it says so instead of quietly doing nothing.
func publishSocket(_, _ string) (func(), error) {
	return func() {}, errors.New(
		"a daemon's socket cannot be bound into a step on this platform, and no" +
			" step here should have been given a daemon to begin with")
}
