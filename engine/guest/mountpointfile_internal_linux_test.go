package guest

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/fstime"
	"github.com/EarthBuild/earthbuild/engine/nstest"
)

// A mount point this engine made as a *file* is taken away again.
//
// The rule is already written down and was already kept for directories: "a
// mount point is not something the step produced", and an empty `/cache` left
// behind was E33. A bind needs something to land on, so the setup creates one -
// and only the directory branch remembered that it had.
//
// Every step therefore captured the sandbox's plumbing as its own output:
//
//	/etc/resolv.conf
//	/dev/full  /dev/tty  /dev/null  /dev/zero  /dev/random  /dev/urandom
//
// seven entries in the delta of `RUN true`, which writes nothing. They are
// created when the step starts, so each carries that moment, and a step that
// does nothing produced a different layer every time it ran - a permanent miss
// on the commonest step there is (E547).
// resolverPath is the one mount point every step gets and no image ships.
const resolverPath = "/etc/resolv.conf"

// Not parallel: mounts.
func TestAFileMountPointThisEngineMadeIsRemoved(t *testing.T) {
	if !nstest.In(t) {
		return
	}

	root, store := t.TempDir(), t.TempDir()

	// A file the step's filesystem does not have, which is the case every
	// device node and the resolver configuration are in.
	source := filepath.Join(t.TempDir(), "resolv.conf")

	err := os.WriteFile(source, []byte("nameserver 127.0.0.1\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	undo, err := bindMounts(root, store, layerStoreForTest(t), []Mount{
		{Sandbox: source, Target: resolverPath, ReadOnly: true, Mode: 0o644},
	})
	if err != nil {
		t.Fatalf("binding the resolver: %v", err)
	}

	at := filepath.Join(root, "etc", "resolv.conf")

	_, err = os.Lstat(at)
	if err != nil {
		t.Fatalf("the mount point was not created: %v", err)
	}

	undo()

	_, err = os.Lstat(at)
	if err == nil {
		t.Error("a mount point this engine created is still there after the" +
			" unmount:\n  it is not something the step produced, so the step's" +
			" layer now records it - and it was made when the step started, so" +
			" the layer is different every run")
	}
}

// A mount point the image already had is left alone.
//
// The other half of the rule, and the reason the setup asks before creating:
// afterwards there is no way to tell. An image that ships `/etc/resolv.conf`
// keeps it, with whatever it contained - removing it would delete a file the
// build is entitled to.
// Not parallel: mounts.
func TestAFileMountPointTheImageHadSurvives(t *testing.T) {
	if !nstest.In(t) {
		return
	}

	root, store := t.TempDir(), t.TempDir()

	err := os.MkdirAll(filepath.Join(root, "etc"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	at := filepath.Join(root, "etc", "resolv.conf")

	err = os.WriteFile(at, []byte("the image's own\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	source := filepath.Join(t.TempDir(), "resolv.conf")

	err = os.WriteFile(source, []byte("nameserver 127.0.0.1\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	undo, err := bindMounts(root, store, layerStoreForTest(t), []Mount{
		{Sandbox: source, Target: resolverPath, ReadOnly: true, Mode: 0o644},
	})
	if err != nil {
		t.Fatalf("binding the resolver: %v", err)
	}

	undo()

	b, err := os.ReadFile(at)
	if err != nil {
		t.Fatalf("a file the image shipped was removed with the mount: %v", err)
	}

	if string(b) != "the image's own\n" {
		t.Errorf("the image's file now holds %q", b)
	}
}

// The directory a mount point was made in keeps the time it had.
//
// Removing the mount point is not enough. Creating an entry in a directory
// changes that directory's mtime, and removing it changes it again - so a
// parent that the engine only ever passed through comes out carrying the moment
// the step started, and overlayfs has copied it up into the delta by then. Two
// empty directories, `/etc` and `/dev`, were what remained of E547 and were
// enough to give `RUN true` a different identity on every machine.
//
// The rule is exact rather than a guess: a directory's mtime reflects entries
// being added and removed, so if the set of names is the same before this
// engine made its mount point and after it took it away, the net effect on that
// directory was nothing and its time is restored. A step that wrote there
// changes the set, and then the time is the step's and is left alone.
// Not parallel: mounts.
func TestTheDirectoryAMountPointWasMadeInKeepsItsTime(t *testing.T) {
	if !nstest.In(t) {
		return
	}

	root, store := t.TempDir(), t.TempDir()

	etc := filepath.Join(root, "etc")

	err := os.MkdirAll(etc, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	when := time.Unix(1_600_000_000, 0)

	err = fstime.Lchtimes(etc, when, when)
	if err != nil {
		t.Fatal(err)
	}

	source := filepath.Join(t.TempDir(), "resolv.conf")

	err = os.WriteFile(source, []byte("nameserver 127.0.0.1\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	undo, err := bindMounts(root, store, layerStoreForTest(t), []Mount{
		{Sandbox: source, Target: resolverPath, ReadOnly: true, Mode: 0o644},
	})
	if err != nil {
		t.Fatalf("binding the resolver: %v", err)
	}

	undo()

	fi, err := os.Lstat(etc)
	if err != nil {
		t.Fatal(err)
	}

	if !fi.ModTime().Equal(when) {
		t.Errorf("the directory holding the mount point carries %v and had %v"+
			"\n  nothing was added to it or taken from it that outlived the"+
			"\n  step, so its time is not the step's to change - and overlayfs"+
			"\n  has copied it into the delta, which makes the step's layer"+
			"\n  different on every machine", fi.ModTime().UTC(), when.UTC())
	}
}

// A directory the step wrote in keeps the step's time, not the one it had.
//
// The other half of the rule, and the reason it is stated as a set of names
// rather than as "put it back": a step that creates a file in `/etc` has
// changed that directory, and restoring the time it had before would hide what
// the step did.
// Not parallel: mounts.
func TestADirectoryTheStepWroteInKeepsTheStepsTime(t *testing.T) {
	if !nstest.In(t) {
		return
	}

	root, store := t.TempDir(), t.TempDir()

	etc := filepath.Join(root, "etc")

	err := os.MkdirAll(etc, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	when := time.Unix(1_600_000_000, 0)

	err = fstime.Lchtimes(etc, when, when)
	if err != nil {
		t.Fatal(err)
	}

	source := filepath.Join(t.TempDir(), "resolv.conf")

	err = os.WriteFile(source, []byte("nameserver 127.0.0.1\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	undo, err := bindMounts(root, store, layerStoreForTest(t), []Mount{
		{Sandbox: source, Target: resolverPath, ReadOnly: true, Mode: 0o644},
	})
	if err != nil {
		t.Fatalf("binding the resolver: %v", err)
	}

	// The step, writing where the engine also happens to have a mount point.
	err = os.WriteFile(filepath.Join(etc, "written-by-the-step"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	undo()

	fi, err := os.Lstat(etc)
	if err != nil {
		t.Fatal(err)
	}

	if fi.ModTime().Equal(when) {
		t.Error("the directory was put back to the time it had before the step" +
			" wrote a file into it:\n  the step changed what it contains, so the" +
			" change is the step's and belongs in its layer")
	}
}
