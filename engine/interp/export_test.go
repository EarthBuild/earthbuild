//go:build darwin

package interp_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// TestSaveArtifactReachesTheHost is what a build is for: a file made inside a
// sandbox ends up on the user's disk.
func TestSaveArtifactReachesTheHost(t *testing.T) {
	t.Parallel()

	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	// The command's meaning depends on its quoting: the redirect belongs to the
	// *inner* shell. A build that loses the quotes still succeeds and writes an
	// empty file, so asserting the exit code proves nothing and asserting the
	// contents proves everything.
	p, err := interp.Build(`VERSION 0.8

build:
    FROM alpine:3.22
    RUN /bin/busybox sh -c "echo produced-by-the-build | /bin/busybox tr a-z A-Z > /out.txt"
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	sb := exec.NewApple()
	sb.GuestBinary = guestd(t)

	err = sb.Available()
	if err != nil {
		t.Skipf("apple container backend unavailable: %v", err)
	}

	// The VM outlives Close by design, so a test whose sandbox is named after a
	// temporary directory has to take it away - nothing will ever name that one
	// again. Without this each run left a VM and its 1.3GB volume behind (E526).
	defer func() { _ = sb.Remove() }()

	e, err := exec.New(sb)
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	e.Platform = "linux/arm64"

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "vm", IsInvoker: true}},
		Executor: e,
		Cache:    memCache{},
		Blobs:    store.LayerStore(sb.StoreDir()),
		Writer:   "test",
	}

	sched, err := s.Run(t.Context(), p.Graph)
	if err != nil {
		t.Fatal(err)
	}

	if len(sched) == 0 {
		t.Fatal("nothing was scheduled")
	}

	// Written into the test's own directory, not the repository.
	dest := filepath.Join(t.TempDir(), "out.txt")

	for _, a := range p.Artifacts {
		stack := s.StackFor(a.From)
		if len(stack) == 0 {
			t.Fatalf("no layer stack for the step at %s", a.Source)
		}

		err := e.Export(t.Context(), stack, a.Path, dest, a.IfExists)
		if err != nil {
			t.Fatalf("%s: %v", a.Source, err)
		}
	}

	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}

	// Exact contents, not "not empty": a pipeline that half-worked would still
	// produce something.
	if got := string(b); got != "PRODUCED-BY-THE-BUILD\n" {
		t.Errorf("artifact contains %q, want the piped and upper-cased text", got)
	}
}
