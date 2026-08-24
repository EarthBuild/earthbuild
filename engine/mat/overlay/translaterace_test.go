//go:build linux

package overlay

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

// markedLayer writes a layer carrying a deletion marker, so translation runs.
func markedLayer(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "kept.txt"), []byte("payload\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(dir, ".wh.gone.txt"), nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return dir
}

// Two builds translating one layer do not delete each other's work.
//
// The translator builds a layer's translated form beside its name and renames
// it in - *"a partial translation must never be stacked, so it is built beside
// its name and renamed in - the rule the layer store itself follows"* - and the
// staging name is `<id>.partial`, which is **the same path for every builder of
// that layer**.
//
// So two builds translating one layer: the second `os.RemoveAll(tmp)` deletes
// the first's half-written tree, and what the first then renames into place is
// whatever survived. The lock above it is `t.mu`, one per materialiser, which
// is exactly no help across two builds sharing a scratch directory.
//
// The same shape as the mount names (E140): **a name that has to be unique,
// derived rather than asked for.** Derived names are unique among the things
// the deriver knows about, and a second process is not one of them.
//
// This is one of the causes filed under E141, and it is the one that was found
// by reading rather than by suspecting: the farm of short names was the
// suspect, and it never unlinks anything.
func TestTwoTranslationsOfOneLayerDoNotCollide(t *testing.T) {
	t.Parallel()

	src := markedLayer(t)
	// Translation turns `.wh.<name>` into a character device, and `mknod` of one
	// is refused wherever the caller does not own the device - a user namespace
	// (measured, E157) and a build container both. Without this gate the test
	// failed there with eight copies of "operation not permitted", which says
	// nothing about whether two translators collide.
	//
	// The probe is the operation, not the uid: euid is 0 in a container and the
	// call is still refused.
	err := canMakeWhiteout(t)
	if err != nil {
		t.Skipf("this environment cannot create a whiteout device, so no"+
			" translation can run here: %v", err)
	}

	shared := t.TempDir()

	const id = "0123456789abcdef"

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		outs []string
		errs []error
	)

	// Separate translators over one directory, which is two builds sharing a
	// scratch: the per-materialiser lock does not span them.
	for range 8 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			out, err := (&translator{dir: shared, done: map[string]string{}}).use(src, id)

			mu.Lock()
			outs, errs = append(outs, out), append(errs, err)
			mu.Unlock()
		}()
	}

	wg.Wait()

	for _, err := range errs {
		if err != nil {
			t.Errorf("a translation failed while another translated the same layer: %v", err)
		}
	}

	// And every result is a complete translation, not a tree somebody else was
	// halfway through: the kept file present, the marker gone.
	for _, out := range outs {
		if out == "" {
			continue
		}

		_, err := os.Stat(filepath.Join(out, "kept.txt"))
		if err != nil {
			t.Errorf("%s is missing a file the layer holds: %v", out, err)
		}

		_, err = os.Lstat(filepath.Join(out, ".wh.gone.txt"))
		if err == nil {
			t.Errorf("%s still carries the marker, so it was stacked half-translated", out)
		}
	}
}

// canMakeWhiteout reports whether a whiteout device can be created here.
func canMakeWhiteout(t *testing.T) error {
	t.Helper()

	at := filepath.Join(t.TempDir(), "probe")

	err := unix.Mknod(at, unix.S_IFCHR|0o600, 0)
	if err != nil {
		return err
	}

	return os.Remove(at)
}
