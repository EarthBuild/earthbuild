package guest

import (
	"os"
	"path/filepath"
	"testing"
)

// linkDirFixture makes a layer holding `real/`, and `link` naming it.
func linkDirFixture(t *testing.T) (*Server, fixedHandle, string) {
	t.Helper()

	dir := t.TempDir()

	layerRoot := filepath.Join(dir, "layers", testSrcLayer)

	err := os.MkdirAll(filepath.Join(layerRoot, "real"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(layerRoot, "real", "a.txt"), []byte("inside\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink("real", filepath.Join(layerRoot, "link"))
	if err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	root := t.TempDir()

	return &Server{LayerDir: dir}, fixedHandle{root: root}, layerRoot
}

// A symlink to a directory is followed, and the tree arrives.
//
// Measured against the engine that ships rather than reasoned about, because
// Docker is not a clean guide here - it carries a build context's symlinks as
// links, and this path is not a build context but one target's artifact
// arriving in another. Asked directly:
//
//	build: RUN mkdir real && echo inside > real/a.txt && ln -s real link
//	       SAVE ARTIFACT link
//	probe: COPY +build/link got
//
// the reference produces `got` as a *directory* holding a.txt. It dereferences,
// and it does so eagerly enough that from a build context the same copy fails
// with `"/real": not found` when the target was not in the transferred subset -
// so this is not indifference, it is a decision.
//
// This engine produced a symlink reading `real`, naming nothing in the image
// that received it. The cause is a seam rather than a rule: copyPath resolves
// its source with os.Stat, so a link to a directory correctly takes the
// directory arm - and then filepath.Walk *lstats its own root*, so the first
// entry of the walk is the link again, matched by the symlink case and copied
// as a link. One resolving call and one non-resolving call, three lines apart.
func TestCopyingALinkToADirectoryBringsTheTree(t *testing.T) {
	t.Parallel()

	s, h, _ := linkDirFixture(t)

	err := s.copyIn(h, []string{testSrcLayer}, "link", "/got", copyOpts{AsDir: true})
	if err != nil {
		t.Fatal(err)
	}

	got := filepath.Join(h.root, "got")

	fi, err := os.Lstat(got)
	if err != nil {
		t.Fatalf("nothing arrived at the destination: %v", err)
	}

	if fi.Mode()&os.ModeSymlink != 0 {
		target, _ := os.Readlink(got)
		t.Fatalf("the copy placed a symlink naming %q, not the tree it names", target)
	}

	body, err := os.ReadFile(filepath.Join(got, "a.txt"))
	if err != nil {
		t.Fatalf("the tree behind the link did not arrive: %v", err)
	}

	if string(body) != "inside\n" {
		t.Errorf("the file behind the link reads %q", body)
	}
}

// The destination keeps the link's name, not its target's.
//
// `COPY --dir link /placed` puts the tree at /placed/link. Resolving the link
// early enough would name it /placed/real - the copy would be correct and
// nothing downstream could find it, which is the failure mode that makes this
// worth pinning separately. The resolution is for deciding what to *walk*; the
// name comes from what the Earthfile said.
func TestALinkToADirectoryKeepsItsOwnNameAtTheDestination(t *testing.T) {
	t.Parallel()

	s, h, _ := linkDirFixture(t)

	err := os.MkdirAll(filepath.Join(h.root, "placed"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = s.copyIn(h, []string{testSrcLayer}, "link", "/placed", copyOpts{AsDir: true})
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(filepath.Join(h.root, "placed", "link", "a.txt"))
	if err != nil {
		t.Errorf("the tree is not at /placed/link: %v", err)
	}

	_, err = os.Lstat(filepath.Join(h.root, "placed", "real"))
	if err == nil {
		t.Error("the copy used the link's target as the destination name")
	}
}

// SAVE ARTIFACT of a link to a directory exports the tree.
//
// The sibling call site, and the reason this is fixed in copyPath rather than
// at either caller: `SAVE ARTIFACT link` and `COPY +t/link` reach the same
// three lines, and every previous divergence in this file came from a rule
// written out twice and then maintained once (I8, the mtime clamp, E47).
func TestExportingALinkToADirectoryExportsTheTree(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := t.TempDir()

	err := os.MkdirAll(filepath.Join(root, "out"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(root, "out", "a.txt"), []byte("inside\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink("out", filepath.Join(root, "link"))
	if err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	s := &Server{LayerDir: dir}

	err = s.export(fixedHandle{root: root}, "link", "art", nil)
	if err != nil {
		t.Fatal(err)
	}

	exported := filepath.Join(dir, "exports", "art")

	fi, err := os.Lstat(exported)
	if err != nil {
		t.Fatalf("nothing was exported: %v", err)
	}

	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("the export is a symlink, which names nothing where it is going")
	}

	_, err = os.Stat(filepath.Join(exported, "a.txt"))
	if err != nil {
		t.Errorf("the tree behind the link was not exported: %v", err)
	}
}

// An absolute link target means absolute *inside the layer*, not on this host.
//
// The guest reads these paths with the host's filesystem, but the link text was
// written by a step that saw a chroot. `ln -s /opt/app link` inside a layer
// names that layer's /opt/app; resolved by os.Stat here it names the guest's
// own /opt/app, which is a different machine's idea of the path and outside
// everything A3 confines a step to.
//
// So the resolution is re-rooted, exactly as within() re-roots every other path
// that arrives from an Earthfile. A target with nothing at the re-rooted place
// is a failure that says so, which is the honest answer (I10): the alternative
// is a copy that silently succeeds with the wrong machine's files in it.
func TestAnAbsoluteLinkResolvesInsideTheLayerAndNotOnTheHost(t *testing.T) {
	t.Parallel()

	s, h, layerRoot := linkDirFixture(t)

	// Somewhere real, outside the layer, holding something recognisable.
	outside := t.TempDir()

	err := os.WriteFile(filepath.Join(outside, "leak.txt"), []byte("host\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(outside, filepath.Join(layerRoot, "escape"))
	if err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	err = s.copyIn(h, []string{testSrcLayer}, "escape", "/got", copyOpts{AsDir: true})

	_, statErr := os.Stat(filepath.Join(h.root, "got", "leak.txt"))
	if statErr == nil {
		t.Fatal("the copy followed an absolute link onto the host and took a file with it")
	}

	if err == nil {
		t.Error("a link to nothing inside the layer was copied without complaint")
	}
}

// A link that names itself is refused rather than followed forever.
//
// `ln -s a b; ln -s b a` is two commands in a RUN, and a resolver that loops
// until it finds something that is not a link does not return. The bound is
// what makes the resolution safe to run on a path an Earthfile chose.
func TestALinkCycleIsRefused(t *testing.T) {
	t.Parallel()

	s, h, layerRoot := linkDirFixture(t)

	err := os.Symlink("b", filepath.Join(layerRoot, "a"))
	if err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	err = os.Symlink("a", filepath.Join(layerRoot, "b"))
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)

	go func() { done <- s.copyIn(h, []string{testSrcLayer}, "a", "/got", copyOpts{AsDir: true}) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a symlink cycle was copied without complaint")
		}
	case <-t.Context().Done():
		t.Fatal("the copy did not return on a symlink cycle")
	}
}
