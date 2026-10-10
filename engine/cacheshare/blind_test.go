package cacheshare_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cacheshare"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A machine that cannot see its own cache mounts says so, once.
//
// **The silent degrade this design keeps re-growing.** On a VM backend the store
// is on the guest's block device, so `<store>/mounts/<id>/<scope>` is a host path
// that does not exist - and `Offer` reads a missing directory as "this mount was
// never used here" and returns nothing, which is exactly right for a mount the
// step never touched and exactly wrong for a machine that has no way to look.
//
// An author on a Mac writes `--portable-except` and `--helper`, gets no cache
// sharing and no indication of why. That is the difference between a known
// limitation and a mystery, and it costs one line.
func TestAMachineThatCannotSeeItsCachesSaysSo(t *testing.T) {
	t.Parallel()

	var said bytes.Buffer

	s := cacheshare.New(t.TempDir(), "", &said)
	s.Blind("the store is on the guest's device and this side cannot read it")

	m := ir.Mount{ID: "k", Portable: true, Helper: "./h.wasm"}
	dir := filepath.Join(t.TempDir(), "never-here")

	if err := s.Offer(context.Background(), m, dir, ""); err != nil {
		t.Fatalf("a blind machine failed the build: %v", err)
	}

	if !strings.Contains(said.String(), "guest") {
		t.Errorf("a machine that cannot see its caches said %q", said.String())
	}
}

// Once per build, not once per mount per step.
//
// A build of forty steps over two caches would otherwise print eighty identical
// lines about a limitation that is a property of the machine. Said once is
// advice; said eighty times is noise the reader learns to scroll past, which is
// how the line that matters gets missed.
func TestABlindMachineSaysItOnce(t *testing.T) {
	t.Parallel()

	var said bytes.Buffer

	s := cacheshare.New(t.TempDir(), "", &said)
	s.Blind("cannot read the store from here")

	m := ir.Mount{ID: "k", Portable: true, Helper: "./h.wasm"}

	for range 5 {
		_ = s.Offer(context.Background(), m, filepath.Join(t.TempDir(), "x"), "")
		_ = s.Stock(context.Background(), m, filepath.Join(t.TempDir(), "x"))
	}

	if got := strings.Count(said.String(), "cannot read the store from here"); got != 1 {
		t.Errorf("said it %d times, want once per build", got)
	}
}

// A machine that can see its caches is not told anything.
func TestASightedMachineIsSilent(t *testing.T) {
	t.Parallel()

	var said bytes.Buffer

	s := cacheshare.New(t.TempDir(), "", &said)

	_ = s.Offer(context.Background(),
		ir.Mount{ID: "k", Portable: true, Helper: "./h.wasm"},
		filepath.Join(t.TempDir(), "never-made"), "")

	if said.Len() != 0 {
		t.Errorf("an ordinary machine was told %q", said.String())
	}
}
