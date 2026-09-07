package exec_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// backends is every Sandbox this platform has, unstarted.
//
// Built by a per-platform file, because the constructors are per-platform. The
// tests below are not.
type backend struct {
	name string
	sb   exec.Sandbox
	// avail is the backend's own availability check. Not on the interface:
	// `Sandbox` is what a build talks to, and whether this machine can run one
	// is asked before the build exists.
	avail func() error
}

// Where a sandbox keeps its layers is answerable before it starts.
//
// `StoreDir` is a query about configuration, and every caller asks it before
// anything boots: the L1 cache is opened against that path, and the tests place
// a base layer in it. A backend that fills it in during `Start` answers `""`
// until then - and `""` is not an error, it is the *working directory*, so
// `filepath.Join(store, "layers", id)` quietly becomes a relative path that
// resolves, is created, and is written to.
//
// That is what happened: two `engine/exec` tests wrote their probe binary into
// `engine/exec/layers/` in the source checkout, `Start` then made a fresh
// temporary root without it, and the step failed with `/probe: no such file or
// directory` - a message about the guest's filesystem, describing a mistake in
// the host's.
//
// **The lesson was already learnt and written down, on one of the two
// backends.** `Apple.StoreDir` carries a paragraph explaining exactly this,
// ending *"so the cache would have been opened in the working directory"*. The
// native backend was written afterwards, against the same interface, and
// resolved its root in `Start`.
//
// The failure class, one iteration on from the last: not merely a shared
// definition with one consumer left behind, but **a fix reasoned out, commented,
// and applied to one of two implementations of the same interface**. Which is
// why this test is written against `Sandbox` and iterates - the next backend
// gets asked without anybody remembering to ask it.
func TestAStoreDirIsKnownBeforeAnythingStarts(t *testing.T) {
	t.Parallel()

	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			err := b.avail()
			if err != nil {
				t.Skipf("%s is unavailable here: %v", b.name, err)
			}

			dir := b.sb.StoreDir()

			if dir == "" {
				t.Fatal("the store is unnamed before the sandbox starts," +
					"\n  and \"\" is not an error - it is the working directory," +
					"\n  so a caller joining onto it writes into the checkout")
			}

			if !filepath.IsAbs(dir) {
				t.Errorf("the store is at a relative path %q, which means a different"+
					" directory to every caller with a different working directory", dir)
			}

			fi, err := os.Stat(dir)
			if err != nil {
				t.Errorf("the store is named but is not there: %v", err)

				return
			}

			if !fi.IsDir() {
				t.Errorf("%s is not a directory", dir)
			}
		})
	}
}

// Asking twice gives the same answer.
//
// A lazily-resolved store that makes a fresh temporary directory per call would
// pass the test above and still lose every layer: the caller that fills the
// store and the caller that reads it would be looking at two places.
func TestAStoreDirDoesNotMoveBetweenQuestions(t *testing.T) {
	t.Parallel()

	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			err := b.avail()
			if err != nil {
				t.Skipf("%s is unavailable here: %v", b.name, err)
			}

			first, second := b.sb.StoreDir(), b.sb.StoreDir()
			if first != second {
				t.Errorf("the store moved between two questions:\n  %s\n  %s", first, second)
			}
		})
	}
}
