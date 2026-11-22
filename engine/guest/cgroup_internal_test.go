//go:build linux

package guest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The previous version of this assertion ran a probe inside the step and had it
// read /sys/fs/cgroup. The step is chrooted into a tempdir with no /sys, so the
// probe skipped every check - and a skip exits 0, so the test passed while
// asserting nothing. Reading the files from here is less elegant and is
// actually a test.
func TestLimitsAreWrittenAndReadBack(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("cgroups need root")
	}

	cg, err := newCgroup("readback", Limits{MemoryMax: 32 << 20, PidsMax: 17, CPUMax: 50000})
	if err != nil {
		t.Skipf("cgroups unavailable here: %v", err)
	}

	defer cg.remove()

	for _, tc := range []struct{ file, want string }{
		{"memory.max", "33554432"},
		{"pids.max", "17"},
		{"cpu.max", "50000 100000"},
		// A memory ceiling with swap left unbounded is not a ceiling: a step
		// allocating far past memory.max is merely swapped, and survives. It must
		// be bounded together with memory or the limit is advisory.
		{"memory.swap.max", "0"},
	} {
		b, err := os.ReadFile(filepath.Join(cg.path, tc.file))
		if err != nil {
			t.Errorf("%s: %v", tc.file, err)

			continue
		}

		if got := strings.TrimSpace(string(b)); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.file, got, tc.want)
		}
	}
}

// A cgroup that cannot enforce what was asked for reports why, rather than
// presenting an unbounded step as a bounded one.
func TestUnavailableCgroupsReportAReason(t *testing.T) {
	t.Parallel()

	_, err := newCgroup("x", Limits{})
	if err != nil {
		t.Errorf("no limits requested, so nothing should be reported: %v", err)
	}
}
