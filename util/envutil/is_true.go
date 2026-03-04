package envutil

import (
	"os"
)

// IsTrue returns true if the env variable `k` is set
// to something bash would interpret as true.
func IsTrue(k string) bool {
	switch os.Getenv(k) {
	case "", "0", "false", "FALSE":
		return false
	default:
		return true
	}
}

// LookupEnv checks for newKey first, then falls back to oldKey.
// Returns the value and whether either key was found.
func LookupEnv(newKey, oldKey string) (string, bool) {
	if v, ok := os.LookupEnv(newKey); ok {
		return v, true
	}

	return os.LookupEnv(oldKey)
}

// IsTrueWithFallback checks newKey first, then oldKey, returning true
// if either is set to a truthy value.
func IsTrueWithFallback(newKey, oldKey string) bool {
	if v, ok := os.LookupEnv(newKey); ok {
		switch v {
		case "", "0", "false", "FALSE":
			return false
		default:
			return true
		}
	}

	return IsTrue(oldKey)
}
