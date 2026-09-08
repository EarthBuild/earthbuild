//go:build linux

package exec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Every drive honours the guest's flushes.
//
// **Firecracker's default is `Unsafe`, which discards them.** The guest's
// filesystem believes a journal commit reached stable storage when nothing has
// left the host's page cache, so a VMM that stops without a clean shutdown
// leaves the image holding a mixture of old and new metadata. That is not a
// dirty log XFS can replay - it is corruption, and it presents as
//
//	earth-vmboot: mount the layer store from /dev/vda: structure needs cleaning
//	XFS (vda): Corruption of in-memory data detected. Shutting down filesystem
//
// after which every later build fails at boot, because the store is a cache
// nothing can repair from the host: `xfs_repair` is not in the initramfs and
// the device is not mountable outside the guest.
//
// Found after a night of killing the VMM to end hung builds, which is a fair
// approximation of a crash - and a crash is precisely what a journalling
// filesystem is supposed to survive. It could not, because this setting told
// the hypervisor not to let it.
func TestEveryDriveHonoursTheGuestsFlushes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	f := &Firecracker{
		Kernel:     filepath.Join(dir, "vmlinux"),
		Initrd:     filepath.Join(dir, "initrd"),
		StoreImage: filepath.Join(dir, "store.img"),
		Root:       dir,
	}
	f.exports = filepath.Join(dir, "exports.img")

	at := filepath.Join(dir, "vm.json")

	err := f.writeConfig(at, filepath.Join(dir, "guest.vsock"))
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(at)
	if err != nil {
		t.Fatal(err)
	}

	var cfg struct {
		Drives []struct {
			ID    string `json:"drive_id"`
			Cache string `json:"cache_type"`
		} `json:"drives"`
	}

	err = json.Unmarshal(raw, &cfg)
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Drives) == 0 {
		t.Fatal("the configuration lists no drives")
	}

	for _, d := range cfg.Drives {
		if d.Cache != "Writeback" {
			t.Errorf("drive %q has cache_type %q, wanted Writeback"+
				"\n  the default discards the guest's flushes, so a VMM that is killed"+
				"\n  leaves a store no host tool can repair", d.ID, d.Cache)
		}
	}
}
