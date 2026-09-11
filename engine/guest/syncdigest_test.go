package guest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// overlayHandle is a merged view with a delta that is not it, which is what
// every real handle is and what fixedHandle deliberately is not.
type overlayHandle struct{ root, delta string }

func (h overlayHandle) Root() string  { return h.root }
func (h overlayHandle) Delta() string { return h.delta }
func (h overlayHandle) Release() error {
	return nil
}

func (h overlayHandle) Observations() core.Observation { return core.Observation{} }

// storeLayer puts a tree in the store under its own identity and notes the
// manifest beside it, as a capture does.
func storeLayer(t *testing.T, layerDir string, files map[string]string) ir.NodeID {
	t.Helper()

	scratch := filepath.Join(t.TempDir(), "tree")

	err := os.MkdirAll(scratch, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	for name, what := range files {
		err = os.MkdirAll(filepath.Dir(filepath.Join(scratch, name)), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(filepath.Join(scratch, name), []byte(what), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	took, manifest, err := layer.TakeManifested(scratch)
	if err != nil {
		t.Fatal(err)
	}

	err = os.MkdirAll(filepath.Join(layerDir, "layers"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	// Renamed rather than copied: the manifest describes the tree that was
	// walked, down to its mtimes, and a second copy of it is a different tree.
	err = os.Rename(scratch, filepath.Join(layerDir, "layers", took.ID.String()))
	if err != nil {
		t.Fatal(err)
	}

	store.NoteManifest(layerDir, took.ID, manifest)

	return took.ID
}

// A source file's digest comes out of its layer's manifest, with no read.
func TestASourceDigestComesFromTheLayersManifest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	id := storeLayer(t, dir, map[string]string{"a.txt": "hello"})

	s := &Server{LayerDir: dir}
	d := s.syncDigests("h1", overlayHandle{root: t.TempDir(), delta: t.TempDir()})

	at := filepath.Join(dir, "layers", id.String(), "a.txt")

	got, ok := d.src(at, 5)
	if !ok {
		t.Fatal("the manifest was written and the digest was not found")
	}

	if got == (ir.NodeID{}) {
		t.Error("the digest is zero")
	}

	// A size the manifest does not agree with means the file on disk is not the
	// file the manifest describes, so the digest is not an answer about it.
	_, ok = d.src(at, 99)
	if ok {
		t.Error("a digest was given for a size the manifest disagrees with")
	}
}

// A layer with no manifest beside it has no digests, and the caller reads.
func TestALayerWithNoManifestHasNoDigests(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	unnoted := "0000000000000000000000000000000000000000000000000000000000000000"
	at := filepath.Join(dir, "layers", unnoted, "a.txt")

	err := os.MkdirAll(filepath.Dir(at), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(at, []byte("hello"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{LayerDir: dir}
	d := s.syncDigests("h1", overlayHandle{root: t.TempDir(), delta: t.TempDir()})

	_, ok := d.src(at, 5)
	if ok {
		t.Error("a layer with no manifest handed back a digest")
	}
}

// The destination's digest comes from the base the merged view reads it from.
func TestADestinationDigestComesFromTheBaseBelowIt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	id := storeLayer(t, dir, map[string]string{"a.txt": "hello"})

	root, delta := t.TempDir(), t.TempDir()

	err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{LayerDir: dir, bases: map[string][]ir.NodeID{"h1": {id}}}
	d := s.syncDigests("h1", overlayHandle{root: root, delta: delta})

	want, ok := d.src(filepath.Join(dir, "layers", id.String(), "a.txt"), 5)
	if !ok {
		t.Fatal("the source digest is missing")
	}

	got, ok := d.dst(filepath.Join(root, "a.txt"), 5)
	if !ok {
		t.Fatal("the destination digest is missing")
	}

	if got != want {
		t.Error("the same bytes in the base and in the layer got different digests")
	}
}

// **A path this step wrote is not the base's any more.** The manifest still
// describes what the base held, and the merged view now shows the step's own
// version - so the digest is refused rather than believed.
func TestAPathTheStepWroteHasNoDestinationDigest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	id := storeLayer(t, dir, map[string]string{"a.txt": "hello"})

	root, delta := t.TempDir(), t.TempDir()

	for _, at := range []string{filepath.Join(root, "a.txt"), filepath.Join(delta, "a.txt")} {
		err := os.WriteFile(at, []byte("wrote"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	s := &Server{LayerDir: dir, bases: map[string][]ir.NodeID{"h1": {id}}}
	d := s.syncDigests("h1", overlayHandle{root: root, delta: delta})

	_, ok := d.dst(filepath.Join(root, "a.txt"), 5)
	if ok {
		t.Error("a path in the step's own delta was answered from the base's manifest")
	}
}

// The newest base holding a path decides what is there, as the mount does.
func TestTheNewestBaseDecidesTheDestinationDigest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	older := storeLayer(t, dir, map[string]string{"a.txt": "aaaaa"})
	newer := storeLayer(t, dir, map[string]string{"a.txt": "bbbbb"})

	root, delta := t.TempDir(), t.TempDir()

	err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("bbbbb"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// Oldest first, as green paper §3.2 defines a stack.
	s := &Server{LayerDir: dir, bases: map[string][]ir.NodeID{"h1": {older, newer}}}
	d := s.syncDigests("h1", overlayHandle{root: root, delta: delta})

	want, ok := d.src(filepath.Join(dir, "layers", newer.String(), "a.txt"), 5)
	if !ok {
		t.Fatal("the source digest is missing")
	}

	got, ok := d.dst(filepath.Join(root, "a.txt"), 5)
	if !ok {
		t.Fatal("the destination digest is missing")
	}

	if got != want {
		t.Error("the older layer decided what the merged view holds")
	}
}

// **The digests decide, and the bytes are never opened.**
//
// Proved by a fixture that lies: the destination on disk holds different bytes
// of the same length from the ones its base layer's manifest describes. A copy
// that read both sides would see the difference and write; one that asks the
// store what each side holds is told they agree and leaves the file alone. So
// the file surviving untouched is the mechanism working, and only that.
//
// No real store can be in this state - a layer is what its manifest says - which
// is the point: nothing short of lying to it distinguishes "did not read" from
// "read and agreed".
func TestASyncCopyAnsweredFromManifestsReadsNeitherSide(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := storeLayer(t, dir, map[string]string{"a.txt": "hello"})
	base := storeLayer(t, dir, map[string]string{"a.txt": "hello"})

	root, delta := t.TempDir(), t.TempDir()

	// The lie. Same length, so the size guard is satisfied and the comparison
	// reaches the digests at all.
	err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("HELLO"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{LayerDir: dir, bases: map[string][]ir.NodeID{"h1": {base}}}
	h := overlayHandle{root: root, delta: delta}

	opts := copyOpts{Sync: true, digests: s.syncDigests("h1", h)}

	err = s.copyIn(h, []string{src.String()}, "a.txt", "/a.txt", opts)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(root, "a.txt")) //nolint:gosec // a path this test made
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != "HELLO" {
		t.Errorf("the destination was rewritten to %q, so both sides were read", got)
	}
}

// Without the manifests the bytes decide, which is the same answer by the slow
// road - and the guarantee that a store with no manifests still builds.
func TestASyncCopyWithNoManifestsFallsBackToTheBytes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := storeLayer(t, dir, map[string]string{"a.txt": "hello"})
	base := storeLayer(t, dir, map[string]string{"a.txt": "hello"})

	for _, id := range []ir.NodeID{src, base} {
		err := os.Remove(store.ManifestPath(dir, id))
		if err != nil {
			t.Fatal(err)
		}
	}

	root, delta := t.TempDir(), t.TempDir()

	err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("HELLO"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{LayerDir: dir, bases: map[string][]ir.NodeID{"h1": {base}}}
	h := overlayHandle{root: root, delta: delta}

	opts := copyOpts{Sync: true, digests: s.syncDigests("h1", h)}

	err = s.copyIn(h, []string{src.String()}, "a.txt", "/a.txt", opts)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(root, "a.txt")) //nolint:gosec // a path this test made
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != "hello" {
		t.Errorf("with no manifest the bytes should have decided; the destination holds %q", got)
	}
}

// overlayMat hands back a merged view with a delta that is not it.
type overlayMat struct{ root, delta string }

func (m *overlayMat) Materialise(context.Context, []ir.NodeID) (core.Handle, error) {
	return overlayHandle{root: m.root, delta: m.delta}, nil
}

// **The handler is what arms the oracle, and nothing else does.**
//
// `copyIn` is given the digests rather than finding them, because the handle's
// base stack is the server's to know - so a `COPY --sync` arriving over the
// protocol is the only thing that proves the two are joined up. Without the one
// line in the handler every test above still passes and no build gets faster.
//
// The same lying fixture as TestASyncCopyAnsweredFromManifestsReadsNeitherSide,
// for the same reason.
func TestTheCopyRequestArmsTheDigests(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := storeLayer(t, dir, map[string]string{"a.txt": "hello"})
	base := storeLayer(t, dir, map[string]string{"a.txt": "hello"})

	root, delta := t.TempDir(), t.TempDir()

	err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("HELLO"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{LayerDir: dir, Mat: &overlayMat{root: root, delta: delta}, Unconfined: true}
	ctx := context.Background()

	got := s.handle(ctx, Request{Kind: KindMaterialise, Stack: []string{base.String()}}, nil)
	if got.Err != "" {
		t.Fatalf("materialise: %s", got.Err)
	}

	got = s.handle(ctx, Request{
		Kind: KindCopy, Handle: got.Handle, From: []string{src.String()},
		Path: "a.txt", Dest: "/a.txt", Sync: true,
	}, nil)

	if got.Err != "" {
		t.Fatalf("copy: %s", got.Err)
	}

	held, err := os.ReadFile(filepath.Join(root, "a.txt")) //nolint:gosec // a path this test made
	if err != nil {
		t.Fatal(err)
	}

	if string(held) != "HELLO" {
		t.Errorf("the copy request did not arm the digests: the destination holds %q", held)
	}
}
