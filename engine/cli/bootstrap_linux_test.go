//go:build linux

package cli_test

import (
	"bytes"
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
	"github.com/EarthBuild/earthbuild/engine/exec"
)

// The engine builds the engine on Linux, and both halves of what it built run.
//
// The Linux sibling of the macOS bootstrap, and a stronger claim than that one
// can make: there, the binaries are cross-built for Linux and only the *guest*
// can be exercised. Here the machine and the target are the same, so the
// `earth-native` it produced runs the next build with the `earth-guestd` it
// produced - **the fixed point, not half of it.**
//
// Rootless, which is what makes it worth having: a user namespace grants the
// mount capability where the mount happens and nothing on the host (E98). A
// developer needs no privilege to reach any of this.
//
// A file of its own rather than a shared one, because the two platforms differ
// in what they are setting up and not only in a flag - a VM and a cross-built
// guest there, a user namespace and a native guest here. One function covering
// both would be a conditional pretending to be an abstraction.
func TestTheEngineBuildsItselfOnLinux(t *testing.T) { // not parallel: one store
	if os.Getenv("EARTH_TEST_BOOTSTRAP") == "" {
		t.Skip("set EARTH_TEST_BOOTSTRAP=1 to build the engine with the engine")
	}

	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	// The guest for *this* machine, which is the difference from the macOS
	// case: there it is cross-built for the VM's platform.
	guest := filepath.Join(t.TempDir(), "earth-guestd")

	build := osexec.Command("go", testTarget, "-o", guest,
		"github.com/EarthBuild/earthbuild/cmd/earth-guestd")
	build.Dir = repo

	msg, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("build earth-guestd: %v: %s", err, msg)
	}

	t.Setenv("EARTH_GUESTD", guest)

	store := filepath.Join(t.TempDir(), "store")
	t.Setenv(testCacheDirEnv, store)

	// `go mod` makes its cache read-only on purpose, and this build has a cache
	// mount full of it - so `t.TempDir`'s own cleanup cannot remove the tree and
	// fails the test after every assertion has passed. Registered *after* the
	// TempDir it repairs, because cleanups run last-registered-first.
	t.Cleanup(func() { makeWritable(store) })

	// Asked *after* the guest exists, because "cannot find earth-guestd" is one
	// of the answers this returns and skipping on it would skip on the thing
	// the test is here to provide. The first version asked first and skipped
	// every time.
	err = exec.NewNative().Available()
	if err != nil {
		t.Skipf("the native backend is unavailable here: %v", err)
	}

	var log bytes.Buffer

	err = cli.Run(context.Background(), cli.Options{Dir: repo, Target: "native-engine", Out: &log})
	if err != nil {
		t.Fatalf("the engine could not build itself: %v\n%s", err, log.String())
	}

	built := filepath.Join(repo, testTarget, "linux", runtimeArch(), "earth-guestd")

	head, err := os.ReadFile(built) //nolint:gosec // a path this build wrote
	if err != nil {
		t.Fatalf("no guest binary came out: %v", err)
	}

	// An ELF, checked by its magic rather than by `file`, which the machine
	// this runs on does not have.
	if len(head) < 4 || string(head[:4]) != "\x7fELF" {
		t.Fatal("what came out is not an ELF binary")
	}

	// And now the fixed point: the engine it built, running the guest it built.
	dir := t.TempDir()

	err = os.WriteFile(filepath.Join(dir, testEarthfile), []byte(`VERSION 0.8

probe:
    FROM alpine:3.22
    RUN echo x > /a.txt && rm /a.txt
    RUN if [ -e /a.txt ]; then echo STILL; else echo GONE; fi > /r.txt
    SAVE ARTIFACT /r.txt AS LOCAL r.txt
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	engine := filepath.Join(repo, testTarget, "linux", runtimeArch(), "earth-native")

	second := filepath.Join(t.TempDir(), "store2")
	t.Cleanup(func() { makeWritable(second) })

	run := osexec.Command(engine, "+probe")
	run.Dir = dir
	run.Env = append(os.Environ(),
		"EARTH_GUESTD="+built,
		"EARTH_CACHE_DIR="+second)

	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("the engine this engine built cannot run a build: %v\n%s", err, out)
	}

	body, err := os.ReadFile(filepath.Join(dir, "r.txt")) //nolint:gosec // a path this test made
	if err != nil {
		t.Fatalf("no artifact: %v\n%s", err, out)
	}

	// A deletion, because it is the longest chain in the system and the last
	// thing to have been wrong (E88, E94).
	if strings.TrimSpace(string(body)) != "GONE" {
		t.Errorf("a build run by the engine's own binaries lost a deletion: %q", body)
	}
}

// makeWritable lets a test's cleanup remove a tree a build left read-only.
func makeWritable(root string) {
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // best effort; the cleanup is not the test
		}

		if fi.IsDir() {
			_ = os.Chmod(p, fi.Mode().Perm()|0o700)
		}

		return nil
	})
}
