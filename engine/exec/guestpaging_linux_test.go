//go:build linux

package exec

import (
	"strings"
	"testing"
)

// The guest is told to use huge pages for anonymous memory.
//
// **Because the kernel we build defaults to madvise, and nothing madvises.**
// The config firecracker publishes sets CONFIG_TRANSPARENT_HUGEPAGE_MADVISE,
// so a guest process gets 2 MiB pages only if it asks - and a Go compiler, which
// is what this engine spends its time running, never does. Every allocation it
// makes is therefore backed by 4 KiB pages and every TLB miss walks a full page
// table inside a guest whose walks are themselves nested.
//
// That is the cost the measurements point at: against the namespace backend on
// the same box, a tight CPU loop runs at parity and reading files runs at
// parity, while creating two thousand processes costs 21% more. The penalty
// lands exactly where page tables are walked.
//
// The kernel command line is ours - unlike the host's THP mode, which is a
// global setting needing root, and unlike hugetlbfs, which needs a reserved
// pool. This asks for nothing from the machine the build runs on.
func TestTheGuestIsToldToUseHugePagesForAnonymousMemory(t *testing.T) {
	t.Parallel()

	got := guestBootArgs("ip=off")

	if !strings.Contains(got, "transparent_hugepage=always") {
		t.Errorf("the guest is not told to use huge pages: %q", got)
	}

	// The things a guest cannot boot without stay where they were.
	for _, must := range []string{"console=ttyS0", "reboot=k", "panic=1", "pci=off", "ip=off"} {
		if !strings.Contains(got, must) {
			t.Errorf("the boot line lost %q: %q", must, got)
		}
	}
}
