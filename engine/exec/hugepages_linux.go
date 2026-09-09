//go:build linux

package exec

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// EnvHugePages backs the guest's memory with 2 MiB pages.
//
// **Because the cost the microVM cannot argue away is paging.** Measured
// against the namespace backend on the same box and the same build: a tight CPU
// loop runs at parity, reading files runs at parity, and creating two thousand
// processes costs 21% more inside the guest - the penalty appears exactly where
// page tables are walked and guest memory is faulted in. A compiler does both
// all day, and the step that builds this repository runs about a third slower
// in a guest than out of one.
//
// Huge pages are the lever for that: fewer entries to walk and far less
// second-level paging. Off by default and asked for by name, because the host
// has to have reserved a pool (`vm.nr_hugepages`), and memory taken for that
// pool is memory nothing else on the machine can use.
const EnvHugePages = "EARTH_VM_HUGE_PAGES"

// hugePageMiB is the size of the pages firecracker offers. Its configuration
// spells this "2M" and accepts nothing else.
const hugePageMiB = 2

// wantsHugePages reports whether this build asked for them.
func wantsHugePages() bool {
	switch os.Getenv(EnvHugePages) {
	case "", "0", "false", "no":
		return false
	default:
		return true
	}
}

// roundToHugePage rounds a guest's memory up to a whole number of huge pages.
//
// Firecracker validates this and refuses the configuration otherwise, which
// would read as a machine that will not start rather than as a size that is odd.
func roundToHugePage(mib int) int {
	if mib%hugePageMiB == 0 {
		return mib
	}

	return mib + (hugePageMiB - mib%hugePageMiB)
}

// hugePagesFit reports whether the host's pool can back a guest of this size,
// and says why not when it cannot.
//
// **Firecracker does not fall back.** It maps guest memory with MAP_HUGETLB and
// fails to boot when the pool cannot cover it, reporting only that it could not
// start - so the check is here, where the reason can be said.
func hugePagesFit(meminfo string, needMiB int) (bool, string) {
	free := meminfoValue(meminfo, "HugePages_Free:")
	sizeKB := meminfoValue(meminfo, "Hugepagesize:")

	if sizeKB <= 0 {
		return false, "this host reports no huge page size, so none are configured"
	}

	haveMiB := free * sizeKB / 1024
	if haveMiB < needMiB {
		return false, fmt.Sprintf(
			"the host's huge page pool holds %d MiB and the guest needs %d"+
				"\n  reserve more with `sysctl vm.nr_hugepages=<n>`, or unset %s",
			haveMiB, needMiB, EnvHugePages)
	}

	return true, ""
}

// meminfoValue reads a /proc/meminfo line's number, or -1.
//
// The unit is left to the caller: meminfo states kB for sizes and a bare count
// for page totals, and reading them the same way is how they get confused.
func meminfoValue(meminfo, key string) int {
	for _, line := range strings.Split(meminfo, "\n") {
		if !strings.HasPrefix(line, key) {
			continue
		}

		fields := strings.Fields(strings.TrimPrefix(line, key))
		if len(fields) == 0 {
			return -1
		}

		n, err := strconv.Atoi(fields[0])
		if err != nil {
			return -1
		}

		return n
	}

	return -1
}

// hugePagesFor decides what a guest of this size gets, and says why when it
// gets nothing it asked for.
//
// Degrades rather than refuses: a build that asked for huge pages on a host
// with none is slower than it hoped, which is a better answer than a build that
// will not run.
func hugePagesFor(memMiB int) string {
	if !wantsHugePages() {
		return ""
	}

	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		fmt.Fprintf(os.Stderr, "earthbuild: cannot tell whether this host has"+
			" huge pages, so the guest gets ordinary ones: %v\n", err)

		return ""
	}

	ok, why := hugePagesFit(string(b), memMiB)
	if !ok {
		fmt.Fprintf(os.Stderr, "earthbuild: %s\n", why)

		return ""
	}

	return "2M"
}
