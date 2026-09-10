//go:build linux

package exec_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/cmd/earth-vmboot/vmboot"
	"github.com/EarthBuild/earthbuild/engine/exec"
)

// A microVM sandbox boots and its agent answers.
//
// The whole point of the backend is that `earth-guestd` needs no VM-specific
// code: it speaks over stdin and stdout, and `earth-vmboot` hands it a vsock
// connection as those. This asserts the chain end to end - VMM, kernel,
// initramfs, block device, vsock multiplexer, PID 1, agent - because every link
// of it is a place a guest can boot and never be spoken to.
//
// Skipped unless the machine has the parts: hosted CI runners have no
// `/dev/kvm`, and Firecracker cannot emulate what it needs (I11).
func TestAMicroVMBootsAndItsAgentAnswers(t *testing.T) { // not parallel: boots a VM
	fc := exec.NewFirecracker()
	if err := fc.Available(); err != nil {
		t.Skip("no microVM on this machine: ", err)
	}

	if os.Getenv("EARTH_VM_STORE") == "" {
		t.Skip("set EARTH_VM_STORE to an XFS image for the layer store")
	}

	conn, err := fc.Start(context.Background())
	if err != nil {
		t.Fatalf("the guest did not start: %v", err)
	}

	t.Cleanup(func() {
		if err := fc.Stop(); err != nil {
			t.Errorf("stopping the sandbox: %v", err)
		}
	})

	// The connection is the agent's stdin. Writing a byte it cannot parse makes
	// it object, which is the proof it is *running* - a silent socket would
	// equally mean earth-vmboot never handed over.
	if _, err := conn.Write([]byte{0}); err != nil {
		t.Fatalf("the agent's connection is not writable: %v", err)
	}

	if err := conn.Close(); err != nil {
		t.Errorf("closing the connection: %v", err)
	}
}

// A machine without the parts says which part, rather than failing later.
func TestAMissingKernelIsRefusedWithItsName(t *testing.T) {
	t.Parallel()

	fc := &exec.Firecracker{Binary: "firecracker"}

	err := fc.Available()
	if err == nil {
		t.Skip("this machine has every part, so there is nothing to refuse")
	}

	// Whatever is missing, the message names it: a backend that degrades has to
	// say what would make it work.
	for _, want := range []string{"kvm", "firecracker", "kernel"} {
		if contains(err.Error(), want) {
			return
		}
	}

	t.Errorf("the refusal names no missing part: %v", err)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}

	return false
}

// The store this sandbox names is a directory on **this** machine.
//
// Every caller of `StoreDir` opens it here: the CLI opens the blob store, the
// action cache and the profile store against it before anything boots, and
// `SAVE ARTIFACT` reads the staged artifact off it with an ordinary `os.Lstat`.
// A guest path there names a directory nothing on the host writes to - and
// worse, one that resolves, so the blobs would be written *somewhere* and the
// guest would find none of them.
//
// The guest's own layers are a separate thing on a separate device, addressed
// by the environment. The two are only the same directory on a backend that
// shares a filesystem, which this one cannot: Firecracker has no virtio-fs.
func TestTheStoreIsAHostDirectory(t *testing.T) {
	t.Parallel()

	at := t.TempDir()
	fc := &exec.Firecracker{Store: at}

	if got := fc.StoreDir(); got != at {
		t.Errorf("the sandbox was told to keep its store at %s and answers %s", at, got)
	}
}

// Left unset it is still a host directory, and a durable one.
//
// The store is a cache: it is worth having because the *next* build reads it.
// A temporary directory would answer every question this asks correctly and
// still throw the cache away between builds, so the assertion is that it lives
// under the user's cache directory, which is where Apple's does.
func TestTheDefaultStoreOutlivesTheBuild(t *testing.T) {
	t.Parallel()

	cache, err := os.UserCacheDir()
	if err != nil {
		t.Skip("no user cache directory on this machine: ", err)
	}

	got := (&exec.Firecracker{}).StoreDir()

	if !strings.HasPrefix(got, cache+string(os.PathSeparator)) {
		t.Errorf("the default store is %s, which is not under the cache directory %s"+
			"\n  a store that does not outlive the build is not a cache", got, cache)
	}
}

// A blob placed in the guest is named by a path **the guest** can open.
//
// The two stores are different directories on this backend and only on this
// backend: the host's holds blobs, the action cache and staged exports, and the
// guest's is a block device the host cannot open at all. `PlaceBlob` answers
// for the guest, because what it answers is handed straight to the guest as the
// path to unpack - and answering with the host's own store produced exactly
// that: `open /home/…/.cache/earthbuild/fc-store/blobs/sha256-…: no such file
// or directory`, reported by a guest that had the bytes all along.
func TestAPlacedBlobIsNamedForTheGuest(t *testing.T) {
	t.Parallel()

	fc := &exec.Firecracker{Store: t.TempDir()}

	at := fc.GuestBlob("sha256-abc")

	if !strings.HasPrefix(at, vmboot.StoreAt+"/") {
		t.Errorf("a placed blob is at %s, which is not under the guest's store %s",
			at, vmboot.StoreAt)
	}

	if strings.HasPrefix(at, fc.StoreDir()) {
		t.Errorf("a placed blob is named by the host's store %s, which the guest"+
			" cannot open", fc.StoreDir())
	}
}

// The machine has an entropy device.
//
// **Firecracker gives a guest none unless asked**, and the consequence is in its
// own documentation: applications block on `/dev/random` or `getrandom(2)`, or -
// worse - take what they need from `/dev/urandom` before the pool is seeded and
// generate weak key material. A build fetches over TLS on almost every step.
//
// Asserted on the configuration rather than on a running guest, because the
// failure it prevents is one you cannot see: a key that is weak looks exactly
// like a key.
func TestTheGuestIsGivenEntropy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fc := &exec.Firecracker{Root: dir, Store: t.TempDir(), StoreImage: "/dev/null"}

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

	if _, ok := cfg["entropy"]; !ok {
		t.Error("the machine has no entropy device, so its guest blocks on randomness" +
			" or invents it")
	}
}

// TestTheGuestIsToldToUseHugePages.
//
// **The kernel firecracker publishes a config for defaults to madvise, and
// nothing madvises.** `CONFIG_TRANSPARENT_HUGEPAGE_MADVISE` means a guest
// process gets 2 MiB pages only if it asks for them, and a Go compiler - which
// is what this engine spends its time running - never asks. Every allocation is
// then backed by 4 KiB pages and every TLB miss walks a page table inside a
// guest whose walks are themselves nested.
//
// That is where the measured penalty is, and only there: against the namespace
// backend on one box and one build, a tight CPU loop ran at parity and reading
// files ran at parity, while two thousand process creations cost 21% more in
// the guest.
//
// The command line rather than the host's own setting, which is the reason this
// is affordable: the host's THP mode is global and needs root, and a hugetlbfs
// pool needs memory reserved that nothing else on the machine may use. The
// guest's command line is this engine's to write.
//
// Asserted on the configuration because the alternative is a benchmark, and a
// missing kernel argument shows up there as noise rather than as an absence.
func TestTheGuestIsToldToUseHugePages(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fc := &exec.Firecracker{Root: dir, Store: t.TempDir(), StoreImage: "/dev/null"}

	at := filepath.Join(dir, "vm.json")
	if err := fc.WriteConfigForTest(at, filepath.Join(dir, "guest.vsock")); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(at)
	if err != nil {
		t.Fatal(err)
	}

	var cfg struct {
		BootSource struct {
			BootArgs string `json:"boot_args"`
		} `json:"boot-source"`
	}

	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(cfg.BootSource.BootArgs, "transparent_hugepage=always") {
		t.Errorf("the guest is not told to use huge pages, so every allocation a"+
			" step makes is backed by 4 KiB pages and walked twice"+
			"\n  boot_args: %s", cfg.BootSource.BootArgs)
	}
}
