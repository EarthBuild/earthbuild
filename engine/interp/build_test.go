//go:build darwin

package interp_test

import (
	"os"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestEarthfileBuildsEndToEnd is the whole engine, from text to a process that
// ran: parse, IR, schedule, pull, unpack, VM, chroot, capture.
//
// Nothing is simulated. The only thing between this and `earth build` is the
// command-line front end.
func TestEarthfileBuildsEndToEnd(t *testing.T) {
	t.Parallel()

	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	sb := exec.NewApple()
	sb.GuestBinary = guestd(t)

	err := sb.Available()
	if err != nil {
		t.Skipf("apple container backend unavailable: %v", err)
	}

	// The VM outlives Close by design, so a test whose sandbox is named after a
	// temporary directory has to take it away - nothing will ever name that one
	// again. Without this each run left a VM and its 1.3GB volume behind (E526).
	defer func() { _ = sb.Remove() }()

	p, err := interp.Build(`VERSION 0.8

build:
    FROM alpine:3.22
    RUN /bin/busybox true
    RUN /bin/busybox echo built
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	e, err := exec.New(sb)
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	e.Platform = "linux/arm64"

	rec := &core.Record{}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "vm", IsInvoker: true}},
		Executor: e,
		Cache:    memCache{},
		Blobs:    allBlobs{},
		Writer:   "test",
		Record:   rec,
	}

	_, err = s.Run(t.Context(), p.Graph)
	if err != nil {
		t.Fatal(err)
	}

	if len(rec.Steps) != 3 {
		t.Fatalf("recorded %d steps, want 3", len(rec.Steps))
	}

	for _, r := range rec.Steps {
		if r.Exit != 0 {
			t.Errorf("%s exited %d", r.Meta.Source, r.Exit)
		}

		// Every step is attributed to its line, which is what makes a diagnostic
		// point at the Earthfile rather than at a digest.
		if r.Meta.Source == "" {
			t.Error("a recorded step has no source location")
		}
	}
}

type memCache map[core.Key]core.Entry

func (m memCache) Get(k core.Key) (core.Entry, bool) { e, ok := m[k]; return e, ok }
func (m memCache) Put(k core.Key, e core.Entry)      { m[k] = e }

type allBlobs struct{}

func (allBlobs) Has(ir.NodeID) bool { return true }
