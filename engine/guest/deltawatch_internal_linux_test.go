//go:build linux

package guest

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/fstime"
	"github.com/EarthBuild/earthbuild/engine/nstest"
)

// Whether the step changed a directory is asked of the step's delta.
//
// **Same question, a fraction of the work.** A directory the engine made a
// mount point in has had its time changed by the engine rather than by the
// step, so the time is put back if the step left the directory alone. The way
// to know used to be reading the directory's entry names before and after -
// but that directory is the *merged* view of an overlay, so reading it merges
// every lower layer, twice per step. It was 14.2ms of a 39.5ms step (E639).
//
// A step's writes land in its delta, so an unchanged delta is an unchanged
// directory whatever the layers beneath hold - and a delta is a plain
// directory with nothing to merge.
//
// The two are separate directories here, which is what makes the test say
// something: the merged view is changed and the delta is not, so a mechanism
// still reading the merged view would decline to restore and this fails.
func TestTheStepsDeltaIsWhatDecidesTheRestore(t *testing.T) {
	if !nstest.In(t) {
		return
	}

	root, store, delta := t.TempDir(), t.TempDir(), t.TempDir()

	etc := filepath.Join(root, "etc")
	if err := os.MkdirAll(etc, 0o750); err != nil {
		t.Fatal(err)
	}

	// The delta's own /etc, empty: the step has written nothing there.
	if err := os.MkdirAll(filepath.Join(delta, "etc"), 0o750); err != nil {
		t.Fatal(err)
	}

	when := time.Unix(1_600_000_000, 0)
	if err := fstime.Lchtimes(etc, when, when); err != nil {
		t.Fatal(err)
	}

	source := filepath.Join(t.TempDir(), "resolv.conf")
	if err := os.WriteFile(source, []byte("nameserver 127.0.0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	undo, err := bindMounts(root, store, layerStoreForTest(t), delta, []Mount{
		{Sandbox: source, Target: resolverPath, ReadOnly: true, Mode: 0o644},
	})
	if err != nil {
		t.Fatalf("binding the resolver: %v", err)
	}

	// A file in the merged view that is *not* in the delta - which is what a
	// lower layer looks like from here. The old mechanism would see the
	// directory's names change and keep its hands off the time.
	if err := os.WriteFile(filepath.Join(etc, "from-a-lower-layer"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	undo()

	fi, err := os.Lstat(etc)
	if err != nil {
		t.Fatal(err)
	}

	if !fi.ModTime().Equal(when) {
		t.Errorf("the directory's time is %v, want %v"+
			"\n  the step wrote nothing into its delta, so the only change to"+
			" this directory was the engine's own mount point", fi.ModTime(), when)
	}
}

// And a step that did write into its delta keeps the time it caused.
//
// The other half, and the one that would break quietly: a mechanism that
// restored unconditionally would put the clock back on a directory the build
// genuinely changed, and hide what the build did.
func TestADeltaTheStepWroteInKeepsTheStepsTime(t *testing.T) {
	if !nstest.In(t) {
		return
	}

	root, store, delta := t.TempDir(), t.TempDir(), t.TempDir()

	etc := filepath.Join(root, "etc")
	if err := os.MkdirAll(etc, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(delta, "etc"), 0o750); err != nil {
		t.Fatal(err)
	}

	when := time.Unix(1_600_000_000, 0)
	if err := fstime.Lchtimes(etc, when, when); err != nil {
		t.Fatal(err)
	}

	source := filepath.Join(t.TempDir(), "resolv.conf")
	if err := os.WriteFile(source, []byte("nameserver 127.0.0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	undo, err := bindMounts(root, store, layerStoreForTest(t), delta, []Mount{
		{Sandbox: source, Target: resolverPath, ReadOnly: true, Mode: 0o644},
	})
	if err != nil {
		t.Fatalf("binding the resolver: %v", err)
	}

	// The step writes into /etc, which lands in its delta.
	if err := os.WriteFile(filepath.Join(delta, "etc", "written"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	undo()

	fi, err := os.Lstat(etc)
	if err != nil {
		t.Fatal(err)
	}

	if fi.ModTime().Equal(when) {
		t.Error("the directory's time was put back over a step that wrote in it" +
			"\n  the time is then a lie about what the build did")
	}
}
