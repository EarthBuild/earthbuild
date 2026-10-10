//go:build linux

package exec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The store is fast by default and durable when asked.
//
// **A layer store is a cache, so speed is the right default.** Firecracker's
// `Unsafe` cache does not pass the guest's flushes to the host, which is faster
// and perfectly safe as long as the guest unmounts before the VMM goes: the
// writes themselves have already been issued, and it is the *ordering* a flush
// would impose that is lost. Kill a guest mid-write and the image keeps a
// mixture of old and new metadata - not a dirty log XFS can replay, but
//
//	earth-vmboot: mount the layer store from /dev/vda: structure needs cleaning
//
// after which every later build fails at boot.
//
// So the answer is a clean unmount on the way out and recovery when there was
// not one - not paying a journal flush on every write for a cache that can be
// rebuilt. `Writeback` stays available for somebody who would rather have the
// guarantee than the speed.
func TestTheStoreIsFastByDefaultAndDurableWhenAsked(t *testing.T) {
	// Not parallel: it sets the durability setting to check both modes.
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

	// Nothing set: fast, which is what a cache wants.
	for _, d := range cfg.Drives {
		if d.Cache != "" && d.Cache != "Unsafe" {
			t.Errorf("drive %q has cache_type %q by default, wanted the fast one", d.ID, d.Cache)
		}
	}

	t.Setenv(EnvDurableStore, "1")

	err = f.writeConfig(at, filepath.Join(dir, "guest.vsock"))
	if err != nil {
		t.Fatal(err)
	}

	raw, err = os.ReadFile(at)
	if err != nil {
		t.Fatal(err)
	}

	cfg.Drives = nil

	err = json.Unmarshal(raw, &cfg)
	if err != nil {
		t.Fatal(err)
	}

	for _, d := range cfg.Drives {
		if d.Cache != "Writeback" {
			t.Errorf("drive %q has cache_type %q with %s set, wanted Writeback",
				d.ID, d.Cache, EnvDurableStore)
		}
	}
}

// Both drives use the io_uring engine.
//
// **Firecracker defaults to Sync, which is one host thread per request.** The
// Async engine submits through io_uring instead, and the difference lands
// where this backend spends real time: the export phase, which is 0.504s of a
// 3.6s hot build and is dominated by writing a built artifact through a
// virtio-blk device.
//
// Host-side and guest-transparent - the guest sees an ordinary block device
// either way, so nothing in the kernel config or the agent has an opinion
// about it.
func TestBothDrivesUseTheAsyncEngine(t *testing.T) {
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
			ID     string `json:"drive_id"`
			Engine string `json:"io_engine"`
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
		if d.Engine != "Async" {
			t.Errorf("drive %q uses io_engine %q, wanted Async", d.ID, d.Engine)
		}
	}
}
