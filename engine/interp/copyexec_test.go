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

// TestCopyPutsHostFilesInTheImage is COPY doing its job: a file on the
// developer's disk is readable by a command running in the sandbox, at the path
// the Earthfile asked for and nowhere else.
func TestCopyPutsHostFilesInTheImage(t *testing.T) {
	t.Parallel()

	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	ctx := t.TempDir()
	err := os.MkdirAll(filepath.Join(ctx, testSourceDir), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(ctx, testSourceDir, "hello.txt"), []byte("from the host"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	p, err := interp.Build(`VERSION 0.8

build:
    FROM alpine:3.22
    COPY src/hello.txt /app/
    RUN /bin/busybox test -f /app/hello.txt
    RUN /bin/busybox test ! -e /src/hello.txt
    SAVE ARTIFACT /app/hello.txt AS LOCAL hello.txt
`, "build", interp.WithContext(ctx))
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
	e.Context = ctx

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "vm", IsInvoker: true}},
		Executor: e,
		Cache:    memCache{},
		Blobs:    store.LayerStore(sb.StoreDir()),
		Writer:   "test",
	}

	// The two RUNs are the assertions: the file is at /app/hello.txt, and the
	// context was not merged in at its host path. A failing step fails the build.
	_, err = s.Run(t.Context(), p.Graph)
	if err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "hello.txt")

	for _, a := range p.Artifacts {
		exportErr := e.Export(t.Context(), s.StackFor(a.From), a.Path, dest, a.IfExists, false)
		if exportErr != nil {
			t.Fatal(exportErr)
		}
	}

	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}

	if string(b) != "from the host" {
		t.Errorf("artifact contains %q", b)
	}
}
