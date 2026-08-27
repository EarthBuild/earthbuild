package exec_test

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// guestStep names a path inside the guest.
//
// Deliberately not resolved through the host's PATH, as the local backend's
// steps are: a step's filesystem is its layer stack, so a host-resolved path
// names a binary that is not in it. Resolving on the wrong side of the boundary
// is the class of bug these backends invite.
func guestStep(name, argv string) *ir.Node {
	return &ir.Node{
		Op:   ir.Op{Kind: ir.OpExec, Args: []string{argv}},
		Meta: ir.Meta{Source: "./Earthfile:" + name},
	}
}

// putProbeLayerAt places the probe binary in a layer store and returns the node
// that names it.
//
// The binary comes from EARTH_TEST_PROBE when set, and is built otherwise. The
// environment variable exists because these tests also run inside a stripped
// container with no Go toolchain, where building is not an option and skipping
// silently would hide the backend from CI entirely.
func putProbeLayerAt(t *testing.T, store string) *ir.Node {
	t.Helper()

	n := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{"probe-base"}}}

	dir := filepath.Join(store, "layers", n.ID().String())
	err := os.MkdirAll(dir, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(probeBinary(t)) // built or supplied by this package
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(dir, "probe"), b, 0o755) //nolint:gosec // must be executable
	if err != nil {
		t.Fatal(err)
	}

	return n
}

var (
	probeOnce sync.Once
	probePath string
	probeSkip string
	errProbe  error
)

// probeBinary is the probe, built once for the package and copied per store.
//
// **Nine tests wanted one and each built its own**, at about 0.3 seconds a
// time, because the probe has to land inside the layer store under test and
// building straight into it looked like the shortest path. It is the same
// binary every time - the source and two environment variables decide it - so
// the build is shared and only the copy is per-store.
//
// That also collapses two code paths into one: a supplied probe was already
// read and written, and now a built one is too.
func probeBinary(t *testing.T) string {
	t.Helper()

	probeOnce.Do(compileProbe)

	if probeSkip != "" {
		t.Skip(probeSkip)
	}

	if errProbe != nil {
		t.Fatal(errProbe)
	}

	return probePath
}

// compileProbe supplies the probe from $EARTH_TEST_PROBE or builds it.
//
// The variable exists because these tests also run inside a stripped container
// with no Go toolchain, where building is not an option and skipping silently
// would hide the backend from CI entirely.
func compileProbe() {
	if pre := os.Getenv("EARTH_TEST_PROBE"); pre != "" {
		_, err := os.Stat(pre)
		if err != nil {
			errProbe = fmt.Errorf("EARTH_TEST_PROBE is set to %s: %w", pre, err)

			return
		}

		probePath = pre

		return
	}

	_, err := osexec.LookPath("go")
	if err != nil {
		probeSkip = "no go toolchain and EARTH_TEST_PROBE is unset, so the probe cannot be provided"

		return
	}

	dir, err := os.MkdirTemp("", "probe")
	if err != nil {
		errProbe = fmt.Errorf("make a directory for the probe: %w", err)

		return
	}

	keepUntilTheEnd(dir)

	at := filepath.Join(dir, "probe")

	// Background rather than a test's context: this outlives whichever test
	// asked first, and cancelling it would strand the tests that follow.
	build := osexec.CommandContext(context.Background(), "go", "build", "-o", at,
		"github.com/EarthBuild/earthbuild/engine/exec/testdata/probe")
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+probeArch(), "CGO_ENABLED=0")

	out, buildErr := build.CombinedOutput()
	if buildErr != nil {
		errProbe = fmt.Errorf("build the probe for linux/%s: %w: %s", probeArch(), buildErr, out)

		return
	}

	probePath = at
}

// keepUntilTheEnd is a directory TestMain removes.
//
// Both shared builds here outlive the test that triggered them, so neither can
// use `t.TempDir()`: that one belongs to whichever test happened to be first
// and is removed when that test ends, leaving every later test pointed at a
// path that is no longer there.
func keepUntilTheEnd(dir string) {
	sharedMu.Lock()
	defer sharedMu.Unlock()

	sharedDirs = append(sharedDirs, dir)
}

var (
	sharedMu   sync.Mutex
	sharedDirs []string
)

// TestMain removes what the shared builds left behind, which no test owns.
//
// It also answers as the sleeping helper `TestAnArtifactCanReplaceARunningBinary`
// needs: that test wants a *running binary* to hold a write lock, and the only
// binary certain to run wherever this suite runs is this suite. Dispatched here
// rather than from a `Test` function so the helper costs no skip of its own -
// a helper that skips whenever it is not the helper is a skip on every run, and
// this suite is watched by a ceiling that counts them (E770).
func TestMain(m *testing.M) {
	// The literal, because the test that sets it is in the *internal* test
	// package and this file is in the external one. Named in both places and
	// nowhere else; see exportbusy_test.go.
	if os.Getenv("EARTH_TEST_SLEEP_HELPER") != "" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}

	code := m.Run()

	for _, d := range sharedDirs {
		_ = os.RemoveAll(d)
	}

	os.Exit(code)
}

// probeArch is the architecture the probe must run on: the guest's, which off
// Linux is the VM's and on Linux is this machine's.
func probeArch() string {
	if a := os.Getenv("EARTH_GUEST_ARCH"); a != "" {
		return a
	}

	if runtime.GOOS == "linux" {
		return runtime.GOARCH
	}

	return testArch
}
