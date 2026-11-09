package exec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// storeAtSandbox keeps its layers in a directory on this machine.
type storeAtSandbox struct {
	Sandbox
	dir string
}

func (s storeAtSandbox) StoreDir() string { return s.dir }
func (storeAtSandbox) Confines() bool     { return true }

// A capture of what a host step wrote is the bytes there now.
//
// A context is filed under its node's identity, which covers its content
// because the interpreter digested it while planning. A LOCALLY artifact cannot
// be digested then - the host step has not run - so its node is the same on
// every build. Filed under that, the second build found the first build's layer
// already "held" and was served it: the stale-artifact bug the cache key was
// just fixed for, arriving by the store instead.
func TestAHostCaptureIsNotServedAnEarlierCapture(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	e := &Executor{Context: project, sb: storeAtSandbox{dir: t.TempDir()}}
	n := &ir.Node{Op: ir.Op{Kind: ir.OpLocal, Args: []string{".user-src"}, NoCache: true}}

	write := func(body string) {
		t.Helper()

		err := os.MkdirAll(filepath.Join(project, ".user-src"), 0o750)
		if err == nil {
			err = os.WriteFile(filepath.Join(project, ".user-src", "f"), []byte(body), 0o600)
		}

		if err != nil {
			t.Fatal(err)
		}
	}

	read := func(layer ir.NodeID) string {
		t.Helper()

		b, err := os.ReadFile(filepath.Join(store.LayerStore(e.sb.StoreDir()).Path(layer), ".user-src", "f"))
		if err != nil {
			t.Fatalf("the captured layer has no .user-src/f: %v", err)
		}

		return string(b)
	}

	write("first")

	one, err := e.stageContext(n)
	if err != nil {
		t.Fatal(err)
	}

	write("second")

	two, err := e.stageContext(n)
	if err != nil {
		t.Fatal(err)
	}

	if one.Layer == two.Layer {
		t.Fatal("the second capture was filed as the first, so it carries the first build's files")
	}

	if got := read(two.Layer); got != "second" {
		t.Errorf("the second capture holds %q, want what the host step wrote this time", got)
	}
}

// An absolute WORKDIR after LOCALLY runs the host step there, on this machine.
//
// A host step's Dir is joined onto the project, which is right for `/sub` and
// was wrong for `WORKDIR /tmp/<id>`: the reference's own
// `tests/local+save-local-artifact-with-workdir` ran `chdir <project>/tmp/<id>`.
func TestAHostAbsoluteStepRunsWhereItSays(t *testing.T) {
	t.Parallel()

	want, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	e := &Executor{Context: t.TempDir()}
	n := &ir.Node{Op: ir.Op{Kind: ir.OpHost, Args: []string{"pwd", "-P"}, Dir: want, HostAbs: true}}

	res, err := e.hostStep(t.Context(), n)
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(res.Stdout); got != want {
		t.Errorf("the host step ran in %q, want %q", got, want)
	}
}

// A host-absolute capture reads the file on this machine, not under the project.
func TestAHostAbsoluteCaptureReadsThisMachine(t *testing.T) {
	t.Parallel()

	elsewhere := t.TempDir()

	err := os.WriteFile(filepath.Join(elsewhere, "out"), []byte("ddd"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	rel := strings.TrimPrefix(filepath.Join(elsewhere, "out"), "/")
	e := &Executor{Context: t.TempDir()}
	n := &ir.Node{Op: ir.Op{Kind: ir.OpLocal, Args: []string{rel}, NoCache: true, HostAbs: true}}
	staged := t.TempDir()

	err = e.copyContextInto(n, staged)
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(staged, rel))
	if err != nil || string(b) != "ddd" {
		t.Errorf("the capture staged %q (%v), want the file on this machine", b, err)
	}
}

// A WORKDIR after LOCALLY makes its directory, as a container's does.
//
// The reference creates it: `tests/local+test-locally-workdir` names a fresh
// /tmp/<id> and asserts `pwd` is it, and `earth` passes it, leaving the
// directory behind.
func TestAHostWorkdirIsMadeIfAbsent(t *testing.T) {
	t.Parallel()

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(root, "not", "yet")
	e := &Executor{Context: t.TempDir()}
	n := &ir.Node{Op: ir.Op{Kind: ir.OpHost, Args: []string{"pwd", "-P"}, Dir: want, HostAbs: true}}

	res, err := e.hostStep(t.Context(), n)
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(res.Stdout); got != want {
		t.Errorf("the host step ran in %q, want %q", got, want)
	}
}

// A host step's PWD is the directory it runs in.
//
// It inherited this process's PWD - the directory `earth` was started in - so a
// shell saw a PWD that was not its cwd, discarded it, and printed the physical
// path: on macOS `/tmp` is `/private/tmp`, and
// `tests/local+test-locally-workdir`'s `test "$(pwd)" = /tmp/<id>` failed.
func TestAHostStepsPwdIsItsDirectory(t *testing.T) {
	t.Parallel()

	want := t.TempDir() // under /var/folders on macOS, which is /private/var/folders
	e := &Executor{Context: t.TempDir()}
	n := &ir.Node{Op: ir.Op{Kind: ir.OpHost, Args: []string{"/bin/sh", "-c", "pwd"}, Dir: want, HostAbs: true}}

	res, err := e.hostStep(t.Context(), n)
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(res.Stdout); got != want {
		t.Errorf("pwd in the host step is %q, want %q - the directory as the Earthfile named it", got, want)
	}
}

// A relative build context still gives a host step an absolute PWD.
//
// `rth +t` run beside its Earthfile has Context "."; the step ran there with
// PWD=".", which a shell keeps as given, so `pwd` printed "." where the
// reference prints the directory - and `LET here = "$(pwd)"` carried the dot
// into a path.
func TestAHostStepsPwdIsAbsoluteUnderARelativeContext(t *testing.T) {
	t.Parallel()

	want, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	e := &Executor{Context: "."}
	n := &ir.Node{Op: ir.Op{Kind: ir.OpHost, Args: []string{"/bin/sh", "-c", "pwd"}}}

	res, err := e.hostStep(t.Context(), n)
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(res.Stdout); got != want {
		t.Errorf("pwd in the host step is %q, want the absolute %q", got, want)
	}
}
