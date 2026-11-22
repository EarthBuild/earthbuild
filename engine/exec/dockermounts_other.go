//go:build !darwin && !linux

package exec

import "errors"

// dockerFor refuses: this platform has no sandbox backend at all, so there is
// nothing to take a daemon from and nowhere to start one.
func dockerFor(bool, string, string) (dockerPlan, error) {
	return dockerPlan{}, errors.New(
		"WITH DOCKER needs a sandbox backend, and this platform has none")
}
