//go:build !unix

package guest

import "time"

// lchtimes has nothing to set where there are no symlinks worth timing.
func lchtimes(string, time.Time, time.Time) error { return nil }
