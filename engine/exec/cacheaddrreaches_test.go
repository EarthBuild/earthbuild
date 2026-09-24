package exec

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guestd"
	"github.com/EarthBuild/earthbuild/internal/sourceguard"
)

// Both backends hand the guest the address it serves the remote cache on.
//
// **A setting absent from a backend's list is ignored inside the VM**, which
// the Linux list's own comment records as having made fifteen of them look like
// they had no effect. This one would look like the service simply does not
// exist: the agent never starts a listener, a client inside a step connects to
// nothing, and the only symptom is a remote execution that quietly never
// happens.
func TestBothBackendsPassTheCacheAddress(t *testing.T) {
	t.Parallel()

	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this package")
	}

	found, err := sourceguard.NonTestFilesContaining(filepath.Dir(here), "guestd.EnvCacheAddr")
	if err != nil {
		t.Fatal(err)
	}

	for _, backend := range []string{"usernet_linux.go", "apple_darwin.go"} {
		if found[backend] == 0 {
			t.Errorf("%s does not pass %s to the guest, so the agent never"+
				"\n  starts a listener and a client inside a step connects to"+
				"\n  nothing - a remote execution that quietly never happens",
				backend, guestd.EnvCacheAddr)
		}
	}
}
