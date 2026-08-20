package exec

import "os"

// containerMarkers are the files a container runtime leaves behind.
//
// Docker's and Podman's. Which runtime made the container is not this engine's
// business - a build inside either is equally inside an outer step - so both
// count and neither is preferred.
var containerMarkers = []string{"/.dockerenv", "/run/.containerenv"}

// inContainer reports whether this build is running inside a container.
//
// **Read, not inferred.** The old trick of looking for `docker` or `kubepods` in
// `/proc/self/cgroup` stopped working with cgroup v2, where the path is commonly
// `0::/` either way. A probe that answered "no" on a modern host would send an
// inner build down the machine's-daemon path and have it refused for a reason
// that is not true, which is worse than not asking.
func inContainer(exists func(string) bool) bool {
	for _, m := range containerMarkers {
		if exists(m) {
			return true
		}
	}

	return false
}

// hereInContainer answers for this process.
func hereInContainer() bool {
	return inContainer(func(p string) bool {
		_, err := os.Stat(p)

		return err == nil
	})
}
