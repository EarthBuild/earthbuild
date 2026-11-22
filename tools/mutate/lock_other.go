//go:build !unix

package main

// lockSweep is a no-op where there is no flock: this tool is developed on unix,
// and a lock that cannot be dropped on a killed process is worse than none.
func lockSweep(string) (func(), error) { return func() {}, nil }
