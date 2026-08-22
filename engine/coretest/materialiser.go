// Package coretest holds conformance suites for the engine's ports.
//
// A port with two implementations - overlayfs on Linux, a guest agent on macOS,
// a simulator everywhere - needs its contract written once and run against all
// of them. containerd does the same for snapshotters and content stores, and it
// is the reason a third-party snapshotter can be trusted: the suite is the
// specification, and passing it is the claim.
//
// Writing the suite before the real implementations exist is deliberate. A
// contract derived from whichever implementation landed first encodes that
// implementation's accidents.
package coretest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// MaterialiserFactory builds an implementation under test, plus its cleanup.
type MaterialiserFactory func(t *testing.T) (core.Materialiser, func())

// LayerBuilder is implemented by materialisers that can be given real layer
// content. Those get the content-level tests as well; a simulator that holds no
// bytes legitimately cannot, and skips them.
//
// This exists because the structural tests alone are too weak: "reversed stacks
// produce different roots" is trivially satisfied by any implementation that
// gives each handle a fresh directory, so it does not actually check that
// layer order is honoured. Only reading a file that two layers disagree about
// does that.
type LayerBuilder interface {
	// WriteLayer creates a layer containing the given path -> contents.
	WriteLayer(id ir.NodeID, files map[string]string) error
}

// MaterialiserSuite runs every conformance test against one implementation.
//
//	func TestSim(t *testing.T) {
//	    coretest.MaterialiserSuite(t, func(t *testing.T) (core.Materialiser, func()) {
//	        return &sim.Materialiser{}, func() {}
//	    })
//	}
func MaterialiserSuite(t *testing.T, newM MaterialiserFactory) {
	t.Helper()

	for _, tc := range []struct {
		name string
		fn   func(*testing.T, core.Materialiser)
	}{
		{"empty stack materialises", emptyStack},
		{"root is stable while held", rootIsStable},
		{"same stack materialises equivalently", sameStackSameRoot},
		{"order is significant", orderMatters},
		{"handles are independent", handlesAreIndependent},
		{"release is idempotent", releaseIsIdempotent},
		{"observations are addressable", observationsAddressable},
		{"upper layer wins", upperLayerWins},
		{"stack contents are visible", stackContentsVisible},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, done := newM(t)
			defer done()

			tc.fn(t, m)
		})
	}
}

func layers(n int) []ir.NodeID {
	out := make([]ir.NodeID, n)
	for i := range out {
		out[i][0] = byte(i + 1)
	}

	return out
}

// stack is n layer identities that the implementation actually holds.
//
// **Named ids are not held layers.** These cases used to materialise identities
// nothing had ever written, which passed only because a materialiser made a
// directory for whatever was missing - so a base that never arrived produced an
// empty tree rather than a refusal, and the suite was asserting that as correct.
// It is not: a stack element the store holds neither way is an element that has
// to be fetched (green paper I18).
//
// A simulator holds no bytes and legitimately cannot write one, so it keeps the
// bare identities: for an implementation with no store, "the store does not hold
// it" says nothing.
func stack(t *testing.T, m core.Materialiser, n int) []ir.NodeID {
	t.Helper()

	ids := layers(n)

	b, ok := m.(LayerBuilder)
	if !ok {
		return ids
	}

	for i, id := range ids {
		err := b.WriteLayer(id, map[string]string{fmt.Sprintf("from-layer-%d", i): "x"})
		if err != nil {
			t.Fatalf("write layer %d: %v", i, err)
		}
	}

	return ids
}

// emptyStack: a step with no inputs runs against scratch, and scratch is a
// legitimate stack rather than an error.
func emptyStack(t *testing.T, m core.Materialiser) {
	t.Helper()

	h, err := m.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatalf("empty stack rejected: %v", err)
	}

	defer h.Release()

	if h.Root() == "" {
		t.Error("scratch produced no root; a step still has to run somewhere")
	}
}

// rootIsStable: the root must not move under a step that is using it.
func rootIsStable(t *testing.T, m core.Materialiser) {
	t.Helper()

	h, err := m.Materialise(context.Background(), stack(t, m, 3))
	if err != nil {
		t.Fatal(err)
	}

	defer h.Release()

	if first, second := h.Root(), h.Root(); first != second {
		t.Errorf("root moved while held: %q then %q", first, second)
	}
}

// sameStackSameRoot: materialising identical content twice must yield
// equivalent filesystems. Implementations may share or duplicate, but a step
// cannot be able to tell which.
func sameStackSameRoot(t *testing.T, m core.Materialiser) {
	t.Helper()

	ctx := context.Background()
	st := stack(t, m, 4)

	a, err := m.Materialise(ctx, st)
	if err != nil {
		t.Fatal(err)
	}

	defer a.Release()

	b, err := m.Materialise(ctx, st)
	if err != nil {
		t.Fatal(err)
	}

	defer b.Release()

	if a.Root() == "" || b.Root() == "" {
		t.Fatal("empty root")
	}
}

// orderMatters is the weak, structural form: reversed stacks are not treated as
// the same thing. upperLayerWins is the real test, for implementations that
// hold content.
func orderMatters(t *testing.T, m core.Materialiser) {
	t.Helper()

	ctx := context.Background()

	fwd := stack(t, m, 2)
	rev := []ir.NodeID{fwd[1], fwd[0]}

	a, err := m.Materialise(ctx, fwd)
	if err != nil {
		t.Fatal(err)
	}

	defer a.Release()

	b, err := m.Materialise(ctx, rev)
	if err != nil {
		t.Fatal(err)
	}

	defer b.Release()

	if a.Root() == b.Root() {
		t.Error("reversed stacks share a root; order is not being honoured")
	}
}

// handlesAreIndependent: releasing one handle must not disturb another, or a
// concurrent build tears down its neighbour's filesystem.
func handlesAreIndependent(t *testing.T, m core.Materialiser) {
	t.Helper()

	ctx := context.Background()

	a, err := m.Materialise(ctx, stack(t, m, 2))
	if err != nil {
		t.Fatal(err)
	}

	b, err := m.Materialise(ctx, stack(t, m, 3))
	if err != nil {
		t.Fatal(err)
	}

	defer b.Release()

	err = a.Release()
	if err != nil {
		t.Fatal(err)
	}

	if b.Root() == "" {
		t.Error("releasing one handle invalidated another")
	}
}

// releaseIsIdempotent: cleanup paths run more than once - a defer plus an
// explicit call, a retry after a failure - and must not care.
func releaseIsIdempotent(t *testing.T, m core.Materialiser) {
	t.Helper()

	h, err := m.Materialise(context.Background(), stack(t, m, 1))
	if err != nil {
		t.Fatal(err)
	}

	err = h.Release()
	if err != nil {
		t.Fatalf("first release failed: %v", err)
	}

	err = h.Release()
	if err != nil {
		t.Errorf("second release failed: %v", err)
	}
}

// observationsAddressable: Observations must be callable and its maps usable
// without a nil check at every call site. Empty until S5, never nil.
func observationsAddressable(t *testing.T, m core.Materialiser) {
	t.Helper()

	h, err := m.Materialise(context.Background(), stack(t, m, 2))
	if err != nil {
		t.Fatal(err)
	}

	defer h.Release()

	obs := h.Observations()
	if obs.Reads == nil || obs.Listings == nil {
		t.Error("Observations returned nil maps; they must be empty, not absent")
	}
}

// upperLayerWins is the test that actually checks ordering: two layers write
// the same path with different contents, and the later one must win.
//
// An implementation treating a stack as a set passes every structural test and
// fails this one. Skipped for materialisers that hold no bytes.
func upperLayerWins(t *testing.T, m core.Materialiser) {
	t.Helper()

	lb, ok := m.(LayerBuilder)
	if !ok {
		t.Skip("materialiser holds no content")
	}

	ctx := context.Background()
	lower, upper := layers(2)[0], layers(2)[1]

	err := lb.WriteLayer(lower, map[string]string{"conflict": "from the lower layer"})
	if err != nil {
		t.Fatal(err)
	}

	err = lb.WriteLayer(upper, map[string]string{"conflict": "from the upper layer"})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		stack []ir.NodeID
		want  string
	}{
		{"upper last", []ir.NodeID{lower, upper}, "from the upper layer"},
		{"lower last", []ir.NodeID{upper, lower}, "from the lower layer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := m.Materialise(ctx, tc.stack)
			if err != nil {
				t.Fatal(err)
			}

			defer h.Release()

			got, err := os.ReadFile(filepath.Join(h.Root(), "conflict"))
			if err != nil {
				t.Fatal(err)
			}

			if string(got) != tc.want {
				t.Errorf("got %q, want %q - layer order is not being honoured", got, tc.want)
			}
		})
	}
}

// stackContentsVisible: every layer's files appear, not merely the last one.
func stackContentsVisible(t *testing.T, m core.Materialiser) {
	t.Helper()

	lb, ok := m.(LayerBuilder)
	if !ok {
		t.Skip("materialiser holds no content")
	}

	st := stack(t, m, 3)
	for i, id := range st {
		err := lb.WriteLayer(id, map[string]string{
			"file" + string(rune('a'+i)): "contents",
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	h, err := m.Materialise(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}

	defer h.Release()

	for _, name := range []string{"filea", "fileb", "filec"} {
		_, err := os.Stat(filepath.Join(h.Root(), name))
		if err != nil {
			t.Errorf("%s missing from the merged view: %v", name, err)
		}
	}
}
