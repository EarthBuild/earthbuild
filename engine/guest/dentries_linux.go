//go:build linux

package guest

import (
	"os"
	"strconv"
	"strings"
)

// defaultDentryLimit is the number of cached names at which a sandbox lets go.
//
// **Under a ceiling that cannot be asked about.** A shared store costs the
// *host* one open descriptor per name the guest has looked up, held until the
// guest's dentry cache evicts it (E540). The limit that ends a build is inside
// the virtiofs device rather than in either kernel, so nothing can query it or
// see it coming (E559) - what is known is where it happens. Measured on the
// build that hits it:
//
//	names cached in the guest   181,133
//	descriptors held on the host 112,063
//
// and the failure is a little above that. A hundred thousand leaves room for a
// second step walking at the same time and still relieves long before the wall.
const defaultDentryLimit = 100_000

// relieveDentries drops the guest's cached names when too many have built up.
//
// **Measuring the thing rather than counting the operations.** The guest can
// read how many names it holds, which is what the host pays for; counting
// walks would be a model of that, and a model that is wrong about one caller
// is a build that fails anyway.
//
// A trade, and the cheaper side of it. Releasing costs the next walk a cold
// cache - 201µs a file against 96µs warm, on a Go toolchain (E551) - while not
// releasing costs the build, which is what `+earthly` did on a tree with a
// large `node_modules`. Slower beats stopped.
//
// Best effort throughout. A guest that cannot read the one or write the other
// keeps its descriptors, which is where it started.
func relieveDentries() {
	limit := defaultDentryLimit

	if raw := os.Getenv(EnvDentryLimit); raw != "" {
		n, err := strconv.Atoi(raw)
		if err == nil {
			limit = n
		}
	}

	if limit <= 0 {
		return
	}

	if cachedNames() < limit {
		return
	}

	// Names and inodes, not the page cache: what holds a host descriptor is the
	// name the guest looked up, and dropping file contents as well would pay
	// for reads this is not trying to avoid.
	_ = os.WriteFile("/proc/sys/vm/drop_caches", []byte("2\n"), 0o200)
}

// cachedNames is how many names this guest is holding, or zero if it cannot
// tell - which reads as "nothing to relieve" and leaves the sandbox as it was.
func cachedNames() int {
	b, err := os.ReadFile("/proc/sys/fs/dentry-state")
	if err != nil {
		return 0
	}

	// nr_dentry first, then nr_unused and three more this does not use.
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0
	}

	n, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0
	}

	return n
}
