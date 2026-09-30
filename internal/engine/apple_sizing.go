package engine

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// minAppleContainerVersion is the oldest `container` CLI that can start a
// privileged container the way RunContainer does: `--cap-add` arrived in
// 0.12.0 (apple/container#1383) and `--read-only-path`/`--masked-path` in
// 1.2.1 (apple/container#2069).
const minAppleContainerVersion = "1.2.1"

// appleMemoryFloorMiB is the least a BuildKit VM is given, whatever the host.
const appleMemoryFloorMiB = 16 * 1024

// containerMemoryFor is the memory ceiling for a BuildKit VM on a host with
// the given physical memory: half of it, and never less than 16 GiB.
//
// **Every build step runs in this one VM**, so its ceiling is the whole build's.
// A compile that hits it is killed by the guest kernel, and what reaches the
// user is `rustc was terminated by a deadly signal` - nothing names memory. A
// quarter of a 16 GiB Mac is 4 GiB, which a Rust or C++ build exceeds.
//
// **A ceiling, not a reservation**: an idle VM with a 16 GiB ceiling holds about
// half a gigabyte on the host, so a floor above a small machine's memory costs
// nothing until something uses it. What it does cost is the high-water mark:
// `container` gives the guest no balloon device, so memory a build touched stays
// with the VM until the VM stops, however much the guest frees. That is why the
// ceiling is paired with an idle exit (see BUILDKIT_IDLE_EXIT_SECONDS in
// buildkitd/entrypoint.sh) rather than kept low.
func containerMemoryFor(hostBytes uint64) string {
	half := hostBytes / 2 / (1 << 20)

	return fmt.Sprintf("%dM", max(uint64(appleMemoryFloorMiB), half))
}

var appleVersionRE = regexp.MustCompile(`version\s+(\d+)\.(\d+)\.(\d+)`)

// checkAppleContainerVersion refuses a `container` CLI older than
// minAppleContainerVersion, given what `container --version` printed.
//
// An answer that does not parse is not refused: a CLI that changed how it
// prints its version would otherwise be turned away for a reason that is not
// true, and the start that follows reports whatever is actually wrong.
func checkAppleContainerVersion(out string) error {
	have, ok := parseAppleVersion(out)
	if !ok {
		return nil
	}

	want, _ := parseAppleVersion("version " + minAppleContainerVersion)
	if !versionLess(have, want) {
		return nil
	}

	return fmt.Errorf("the container CLI is %d.%d.%d, and EarthBuild needs %s or later"+
		"\n  BuildKit runs privileged, and --read-only-path/--masked-path first appeared in %s"+
		"\n  upgrade from https://github.com/apple/container/releases, then `container system start`",
		have[0], have[1], have[2], minAppleContainerVersion, minAppleContainerVersion)
}

func parseAppleVersion(out string) ([3]int, bool) {
	m := appleVersionRE.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return [3]int{}, false
	}

	var v [3]int

	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return [3]int{}, false
		}

		v[i] = n
	}

	return v, true
}

func versionLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}

	return false
}
