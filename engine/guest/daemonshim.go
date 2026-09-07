package guest

// daemonShimFlag is the argv[1] that makes this binary a daemon shim rather than
// itself.
//
// Distinctive on purpose: a build that somehow reaches this by accident should
// be obviously diagnosable from `ps`, and no Earthfile can produce the string.
const daemonShimFlag = "--earthbuild-daemon-shim"

// shimArgv is what the shim is invoked with: the flag, the daemon's path, and
// the daemon's own arguments, each a separate entry.
//
// **Never a command line.** The alternative - `unshare -Ur --mount sh -c "mount
// …; exec dockerd …"`, which is what E364 used by hand - requires building a
// shell string out of two paths that arrived over the wire (§5.3). `checkDaemon`
// establishes they are absolute and nothing else, so a path containing a quote
// would stop being a path. Here they are argv entries from end to end and there
// is nothing to reinterpret them.
func shimArgv(dockerd string, args []string) []string {
	out := make([]string, 0, len(args)+2)
	out = append(out, daemonShimFlag, dockerd)

	return append(out, args...)
}
