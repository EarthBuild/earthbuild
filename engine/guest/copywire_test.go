package guest_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// rootMat is a materialiser whose handle is a fixed directory: the step's base.
type rootMat struct{ root string }

func (m *rootMat) Materialise(context.Context, []ir.NodeID) (core.Handle, error) {
	return rootHandle{root: m.root}, nil
}

type rootHandle struct{ root string }

func (h rootHandle) Root() string                   { return h.root }
func (h rootHandle) Delta() string                  { return h.root }
func (h rootHandle) Release() error                 { return nil }
func (h rootHandle) Observations() core.Observation { return core.Observation{} }

// What a copy observed reaches the host.
//
// The guest records it (E119) and the host asks for it over `KindObserve`. Three
// pieces had to agree - the recorder, the wire, and the decoder - and the wire
// had no field for `Incomplete` at all, so a careful sender and a careful
// receiver were separated by a transport that dropped the care.
//
// End to end here rather than in three unit tests, because each of the three was
// individually correct while the whole was not. That is the shape of every
// finding this session: the halves were finished, and the joins were not.
func TestACopysObservationReachesTheHost(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// A source layer to copy from, and a base holding the destination.
	srcID := ir.NodeID{7}
	src := filepath.Join(dir, "layers", srcID.String())
	mkdirAll(t, src)
	writeFile(t, filepath.Join(src, "a.txt"), "hi\n")

	root := filepath.Join(dir, "root")
	mkdirAll(t, filepath.Join(root, "w"))

	c := pairWith(t, &guest.Server{LayerDir: dir, Mat: &rootMat{root: root}, Unconfined: true})

	h, err := c.Materialise(context.Background(), []ir.NodeID{{1}})
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = h.Release() }()

	err = c.Copy(context.Background(), h, []ir.NodeID{srcID}, "a.txt", "/w/", guest.CopyOpts{})
	if err != nil {
		// Not a skip. A fixture the copy cannot run against would make all
		// three of these tests report success while asserting nothing, which
		// is the shape a green gate over missing code has (E90).
		t.Fatalf("the copy did not run: %v", err)
	}

	obs := h.Observations()

	if _, ok := obs.Reads["/w"]; !ok {
		t.Errorf("the destination the guest observed did not reach the host: %v", obs.Reads)
	}

	if obs.Incomplete {
		t.Error("a plain copy arrived marked lossy")
	}
}

// And an admission of loss reaches the host too.
//
// The half the protocol could not express. A guest that knows it missed
// something and a host that would have honoured the flag were connected by a
// `Response` with no field for it - so Κ₂ would have claimed a step read
// exactly the paths recorded, about a step that read more (I3).
func TestAnAdmissionOfLossReachesTheHost(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	srcID := ir.NodeID{7}
	src := filepath.Join(dir, "layers", srcID.String())
	mkdirAll(t, src)
	writeFile(t, filepath.Join(src, "a.txt"), "hi\n")

	root := filepath.Join(dir, "root")
	mkdirAll(t, filepath.Join(root, "w"))

	err := os.Symlink("w", filepath.Join(root, "link"))
	if err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	c := pairWith(t, &guest.Server{LayerDir: dir, Mat: &rootMat{root: root}, Unconfined: true})

	h, err := c.Materialise(context.Background(), []ir.NodeID{{1}})
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = h.Release() }()

	err = c.Copy(context.Background(), h, []ir.NodeID{srcID}, "a.txt", "/link/", guest.CopyOpts{})
	if err != nil {
		t.Fatalf("the copy did not run: %v", err)
	}

	if !h.Observations().Incomplete {
		t.Error("the guest declared its observation lossy and the host decoded" +
			" it as complete: the wire dropped the only field that makes a" +
			" lossy source safe to have")
	}
}

// A negative lookup survives the wire as a negative lookup.
//
// `Negative` is the field a specification recording only reads would omit, and
// a transport that dropped it would be the same omission one layer down.
func TestANegativeLookupReachesTheHost(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	srcID := ir.NodeID{7}
	src := filepath.Join(dir, "layers", srcID.String())
	mkdirAll(t, src)
	writeFile(t, filepath.Join(src, "a.txt"), "hi\n")

	root := filepath.Join(dir, "root")
	mkdirAll(t, root)

	c := pairWith(t, &guest.Server{LayerDir: dir, Mat: &rootMat{root: root}, Unconfined: true})

	h, err := c.Materialise(context.Background(), []ir.NodeID{{1}})
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = h.Release() }()

	err = c.Copy(context.Background(), h, []ir.NodeID{srcID}, "a.txt", "/nowhere/x.txt", guest.CopyOpts{})
	if err != nil {
		t.Fatalf("the copy did not run: %v", err)
	}

	if got := h.Observations().Negative; !slices.Contains(got, "/nowhere") {
		t.Errorf("the absent destination did not reach the host: %v", got)
	}
}

func mkdirAll(t *testing.T, p string) {
	t.Helper()

	err := os.MkdirAll(p, 0o750)
	if err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()

	err := os.WriteFile(p, []byte(body), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}
