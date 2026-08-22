package guest

import (
	"os"
	"path/filepath"
	"testing"

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

func TestAFileMountPointThisEngineMadeIsRemoved(t *testing.T) { //nolint:paralleltest // mounts
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

	undo, err := bindMounts(root, store, []Mount{
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
func TestAFileMountPointTheImageHadSurvives(t *testing.T) { //nolint:paralleltest // mounts
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

	undo, err := bindMounts(root, store, []Mount{
		{Sandbox: source, Target: resolverPath, ReadOnly: true, Mode: 0o644},
	})
	if err != nil {
		t.Fatalf("binding the resolver: %v", err)
	}

	undo()

	b, err := os.ReadFile(at) //nolint:gosec // a path this test made
	if err != nil {
		t.Fatalf("a file the image shipped was removed with the mount: %v", err)
	}

	if string(b) != "the image's own\n" {
		t.Errorf("the image's file now holds %q", b)
	}
}
