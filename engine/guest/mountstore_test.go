package guest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A cache mount lives on storage the guest owns, when there is any.
//
// It was beside the layers, in the store shared from the host, because it has to
// outlive the step. That is the right requirement and the wrong conclusion: a
// cache mount does not need the *host* to see it. Measured in one guest, 4,000
// files: untarring into a block-device volume takes 0.09s where the shared store
// takes 2.31s, and removing the tree 0.00s against 0.62s, because a metadata
// operation on a block device never crosses the VM boundary.
func TestACacheMountPrefersStorageTheGuestOwns(t *testing.T) { //nolint:paralleltest // t.Setenv
	fast := t.TempDir()
	t.Setenv(EnvFast, fast)

	s := &Server{LayerDir: "/var/lib/earthbuild/store"}

	got := s.mountStore()
	if !strings.HasPrefix(got, fast) {
		t.Errorf("cache mounts are at %q, not on the guest's own storage at %q", got, fast)
	}
}

// Without it, they are where they always were.
//
// The volume is a darwin sandbox's arrangement. A Linux worker confines with
// namespaces and has no such device, and a build there must keep working
// exactly as it did.
func TestWithoutOwnedStorageCacheMountsAreBesideTheLayers(t *testing.T) { //nolint:paralleltest // t.Setenv
	t.Setenv(EnvFast, "")

	s := &Server{LayerDir: "/var/lib/earthbuild/store"}

	if got, want := s.mountStore(), filepath.Join("/var/lib/earthbuild/store", "mounts"); got != want {
		t.Errorf("cache mounts moved to %q with no volume; want %q", got, want)
	}
}

// A path that names nothing usable is not used.
//
// The environment says a volume was attached; the filesystem is the authority on
// whether it arrived. A sandbox that started without its mount would otherwise
// put a build's caches somewhere that is not there, and the failure would name a
// cache rather than a missing volume.
func TestAnAbsentVolumeIsNotUsed(t *testing.T) { //nolint:paralleltest // t.Setenv
	t.Setenv(EnvFast, filepath.Join(os.TempDir(), "definitely-not-attached-earthbuild"))

	s := &Server{LayerDir: "/var/lib/earthbuild/store"}

	if got := s.mountStore(); !strings.HasPrefix(got, "/var/lib/earthbuild/store") {
		t.Errorf("cache mounts went to %q, which does not exist", got)
	}
}
