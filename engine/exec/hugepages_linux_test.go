//go:build linux

package exec

import (
	"strings"
	"testing"
)

const meminfo2M = `MemTotal:       65707072 kB
HugePages_Total:    4096
HugePages_Free:     4096
HugePages_Rsvd:        0
Hugepagesize:       2048 kB
`

const meminfoNone = `MemTotal:       65707072 kB
HugePages_Total:       0
HugePages_Free:        0
Hugepagesize:       2048 kB
`

// A guest is backed by huge pages only where the host has enough of them.
//
// **Because firecracker does not fall back.** It maps guest memory with
// MAP_HUGETLB and fails to boot if the pool cannot cover it, so a build on a
// machine with no reservation would get "start firecracker" and a number. The
// check is here so the diagnosis is here.
func TestHugePagesAreUsedOnlyWhereTheHostHasThem(t *testing.T) {
	t.Parallel()

	// 4096 pages of 2 MiB is 8192 MiB.
	ok, why := hugePagesFit(meminfo2M, 8192)
	if !ok {
		t.Errorf("a pool that exactly covers the guest was refused: %s", why)
	}

	ok, why = hugePagesFit(meminfo2M, 8194)
	if ok {
		t.Error("a guest larger than the pool was accepted; firecracker would" +
			" fail to boot and say only that it could not start")
	}

	if !strings.Contains(why, "8192") {
		t.Errorf("the refusal does not say how much the pool holds: %q", why)
	}

	ok, _ = hugePagesFit(meminfoNone, 1024)
	if ok {
		t.Error("a host with no huge pages reserved was accepted")
	}
}

// A guest's memory must be a multiple of the page size, or firecracker refuses
// the configuration outright.
func TestGuestMemoryIsRoundedToTheHugePage(t *testing.T) {
	t.Parallel()

	for in, want := range map[int]int{
		2047: 2048,
		2048: 2048,
		2049: 2050,
	} {
		if got := roundToHugePage(in); got != want {
			t.Errorf("roundToHugePage(%d) = %d, want %d", in, got, want)
		}
	}
}

// Nothing is asked for unless somebody asked.
func TestHugePagesAreOffUnlessAskedFor(t *testing.T) {
	t.Parallel()

	for value, want := range map[string]bool{
		"":      false,
		"0":     false,
		"false": false,
		"no":    false,
		"1":     true,
		"true":  true,
	} {
		t.Setenv(EnvHugePages, value)

		if got := wantsHugePages(); got != want {
			t.Errorf("%s=%q gives %v, want %v", EnvHugePages, value, got, want)
		}
	}
}
