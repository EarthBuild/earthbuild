// Package version holds earth's version strings and build information injected at compile time.
package version

// We use this package to export ldflags main vars to other packages.
var (
	Version string
	GitSha  string
	BuiltBy string
)
