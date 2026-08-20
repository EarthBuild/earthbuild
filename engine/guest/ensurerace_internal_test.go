package guest

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// ensureFile never opens a path it did not create.
//
// E52 already fixed this once: the target is `Lstat`ed and created only when
// missing, because *"opening `/dev/tty` with no controlling terminal returns
// ENXIO, so a build with two concurrent steps failed on every machine without a
// terminal and on none with one"*.
//
// Lstat-then-Open is **check-then-act**. Two steps preparing the same mount
// point: one finds nothing, the other binds `/dev/tty` there, and the first's
// open lands on the device and returns ENXIO. The window is small and the fix
// closed most of it, which is why this survived - it needs concurrency, and a
// machine with no controlling terminal, which is every CI job and every
// non-interactive ssh.
//
// A unix socket stands in for the tty: `open(2)` on one returns ENXIO exactly
// as it does for a tty with no controlling terminal, and a test can create one
// without privileges.
//
// Stress rather than a deterministic interleaving - there is no seam between the
// stat and the open to inject at. It fails within a few hundred iterations
// against the check-then-act version, which is enough to have caught this, and
// with O_EXCL there is no window to hit.
func TestEnsureFileDoesNotOpenWhatItDidNotCreate(t *testing.T) {
	t.Parallel()

	// Short, because a unix socket path is capped near 104 bytes and a
	// t.TempDir name is long.
	//nolint:usetesting // a unix socket path is capped near 104 bytes; see above
	dir, err := os.MkdirTemp("", "e")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	for i := range 400 {
		target := filepath.Join(dir, string(rune('a'+i%26))+string(rune('a'+i/26)))

		var wg sync.WaitGroup

		wg.Add(2)

		var ensureErr error

		go func() {
			defer wg.Done()

			ensureErr = ensureFile(target, 0o600)
		}()

		go func() {
			defer wg.Done()

			// The other step, binding something unopenable at the same path.
			l, err := net.Listen("unix", target)
			if err == nil {
				_ = l.Close()
			}
		}()

		wg.Wait()

		if ensureErr != nil {
			t.Fatalf("iteration %d: ensureFile opened a path another step had just"+
				" made: %v", i, ensureErr)
		}

		_ = os.Remove(target)
	}
}
