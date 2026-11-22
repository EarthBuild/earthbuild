package cli

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// plainSandbox does not say how it shares the store, which is every sandbox
// whose store is simply a directory on this machine.
type plainSandbox struct{ dir string }

func (s plainSandbox) Start(context.Context) (exec.Conn, error) { return nil, nil }
func (s plainSandbox) Stop() error                              { return nil }
func (s plainSandbox) StoreDir() string                         { return s.dir }
func (s plainSandbox) Confines() bool                           { return true }

// sharingSandbox shares its store into a VM, where everything is owned by root.
type sharingSandbox struct{ plainSandbox }

func (s sharingSandbox) SharesStoreAsRoot() bool { return true }

// A sandbox that shares its store as root gets a view that reads it that way.
//
// Κ₂ compares what a step observed against what a rebuilt step would see, and
// both happen inside the sandbox. Where the store is shared into a VM with
// everything owned by root, the guest digests uid 0 for a file the store holds
// as the invoking user - a constant offset that made every base look changed and
// left the tier unable to serve a single RUN on darwin (E494).
//
// `TestTheDarwinSandboxSaysHowItShares` asserts the sandbox *answers* the
// question. Nothing asserted that `viewsFor` uses the answer, so the mutant that
// drops the `SeenAsRoot` wrapper and hands back the bare store survived the
// suite - which is the same defect the experiment is named for, reinstated.
//
// The distinction is by type because that is what there is: `SeenAsRoot` returns
// an unexported wrapper, so "not the bare LayerStore" is the observable, and it
// is enough to tell the two paths apart.
func TestAStoreSharedAsRootIsViewedThatWay(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if _, bare := viewsFor(plainSandbox{dir: dir}).(store.LayerStore); !bare {
		t.Error("a sandbox that says nothing about sharing got a corrected" +
			" view: the correction is for the case where the host knows the" +
			" ownership is shifted, and here nothing is")
	}

	if _, bare := viewsFor(sharingSandbox{plainSandbox{dir: dir}}).(store.LayerStore); bare {
		t.Error("a sandbox sharing its store as root got the bare store:" +
			" every base looks changed to Κ₂ and no RUN is ever served," +
			" which is E494 exactly")
	}
}
