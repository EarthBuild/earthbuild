//go:build !unix

package fstime

import "time"

// Lchtimes cannot stamp a link where the platform has no such call. Left alone
// rather than faked: the caller's digest check disagrees, and a disagreement is
// better than a layer that claims to match.
func Lchtimes(string, time.Time, time.Time) error { return nil }
