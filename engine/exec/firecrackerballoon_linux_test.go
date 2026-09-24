package exec_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// balloonOf writes a machine configuration for a Firecracker of the given
// version and returns its balloon, if it has one.
func balloonOf(t *testing.T, version string) (map[string]any, bool) {
	t.Helper()

	dir := t.TempDir()
	fc := &exec.Firecracker{Root: dir, Store: t.TempDir(), StoreImage: "/dev/null", Version: version}

	at := filepath.Join(dir, "vm.json")
	if err := fc.WriteConfigForTest(at, filepath.Join(dir, "guest.vsock")); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(at)
	if err != nil {
		t.Fatal(err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}

	balloon, ok := cfg["balloon"].(map[string]any)

	return balloon, ok
}

// A guest gives back the memory it frees, where Firecracker can take it.
//
// **Without a balloon a microVM keeps its high-water mark until it stops.**
// Firecracker maps guest memory up front and the pages a build touched stay
// resident in its process however much the guest frees - and the VM is kept
// warm between builds, so that is most of its life. Free page reporting is the
// guest telling the host which ranges it freed, about two seconds after the
// free, and Firecracker `madvise`s them `MADV_DONTNEED`: the host's memory
// falls with no policy of ours to get wrong.
//
// `amount_mib: 0` because the balloon is only the channel: nothing is inflated
// up front, so a step starts with the whole memory it was given.
// `deflate_on_oom` so that, should anything ever inflate it, a guest about to
// kill a compiler takes the memory back first.
func TestAMicroVMReportsTheMemoryItFrees(t *testing.T) {
	t.Parallel()

	for _, version := range []string{"1.14.0", "1.17.0", "2.0.0"} {
		balloon, ok := balloonOf(t, version)
		if !ok {
			t.Errorf("Firecracker %s: no balloon, so the guest's freed memory stays on the host", version)

			continue
		}

		for key, want := range map[string]any{
			"amount_mib":          float64(0),
			"deflate_on_oom":      true,
			"free_page_reporting": true,
		} {
			if balloon[key] != want {
				t.Errorf("Firecracker %s: balloon %s is %v, want %v", version, key, balloon[key], want)
			}
		}
	}
}

// And no balloon at all where Firecracker is too old to report.
//
// Before 1.14 a balloon is a target the host moves, and nothing here moves one,
// so it would do nothing - while `free_page_reporting` is a field an older
// Firecracker rejects, and a rejected field is a VM that does not boot.
func TestAnOlderFirecrackerIsGivenNoBalloon(t *testing.T) {
	t.Parallel()

	for _, version := range []string{"1.13.1", "1.7.0", ""} {
		if balloon, ok := balloonOf(t, version); ok {
			t.Errorf("Firecracker %q was given a balloon %v; it cannot report, and may refuse the field",
				version, balloon)
		}
	}
}
