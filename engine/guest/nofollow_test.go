package guest

import (
	"os"
	"path/filepath"
	"testing"
)

// `COPY --symlink-no-follow` places the link, not the tree it names.
//
// The other half of E74, which made a copy follow a link because that is what
// the reference does by default. The flag asks for the opposite, and E75
// measured that it is the *copy* that decides: with the flag on both sides the
// link arrives and dangles in the receiving image, and with it on the producing
// side alone the tree arrives.
//
// Dangling is the point rather than a defect. `ln -s real link` names a
// sibling, and an image that receives the link without `real` has a link to
// nothing - which is exactly what the author asked for, and what the reference
// produces. An engine that quietly copied the tree instead would be deciding
// the author was mistaken.
func TestCopyingWithoutFollowingPlacesTheLink(t *testing.T) {
	t.Parallel()

	s, h, _ := linkDirFixture(t)

	err := s.copyIn(h, []string{testSrcLayer}, "link", "/got", copyOpts{AsDir: true, NoFollow: true})
	if err != nil {
		t.Fatal(err)
	}

	got := filepath.Join(h.root, "got")

	fi, err := os.Lstat(got)
	if err != nil {
		t.Fatalf("nothing arrived at the destination: %v", err)
	}

	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the copy followed the link, which is what the flag asks it not to do")
	}

	target, err := os.Readlink(got)
	if err != nil {
		t.Fatal(err)
	}

	if target != "real" {
		t.Errorf("the link points at %q, not at what it pointed at in the source", target)
	}
}

// Without the flag it still follows, which is E74's default and unchanged.
//
// The arm that stops the flag being implemented by making everything a link.
func TestCopyingStillFollowsByDefault(t *testing.T) {
	t.Parallel()

	s, h, _ := linkDirFixture(t)

	err := s.copyIn(h, []string{testSrcLayer}, "link", "/got", copyOpts{AsDir: true})
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(filepath.Join(h.root, "got", "a.txt"))
	if err != nil {
		t.Errorf("the default no longer brings the tree: %v", err)
	}
}

// A link placed where something already is replaces it.
//
// A destination is not always empty: a step earlier in the build may have put a
// file or a directory there, and `os.Symlink` fails outright on an existing
// path. The copy has to clear what is in the way - and clear it with
// `os.Remove`, which does not follow a link, so a symlink planted at the
// destination is deleted rather than followed out of the step's root (A3, the
// same rule copyTree follows).
func TestPlacingALinkOverSomethingReplacesIt(t *testing.T) {
	t.Parallel()

	s, h, _ := linkDirFixture(t)

	err := os.WriteFile(filepath.Join(h.root, "got"), []byte("in the way"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = s.copyIn(h, []string{testSrcLayer}, "link", "/got", copyOpts{AsDir: true, NoFollow: true})
	if err != nil {
		t.Fatal(err)
	}

	fi, err := os.Lstat(filepath.Join(h.root, "got"))
	if err != nil {
		t.Fatal(err)
	}

	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the file that was in the way is still there")
	}
}

// A source that is not a link is copied as itself.
//
// The flag says how to treat a link, not to look for one. A plain file or
// directory under `--symlink-no-follow` is the ordinary copy, and an
// implementation that reached for `os.Readlink` unconditionally would fail on
// every source that is not a link - which is nearly all of them.
func TestNotFollowingAnOrdinaryFileIsAnOrdinaryCopy(t *testing.T) {
	t.Parallel()

	s, h, layerRoot := linkDirFixture(t)

	err := os.WriteFile(filepath.Join(layerRoot, "plain.txt"), []byte("body\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = s.copyIn(h, []string{testSrcLayer}, "plain.txt", "/got.txt", copyOpts{NoFollow: true})
	if err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(filepath.Join(h.root, "got.txt")) // a path this test made
	if err != nil {
		t.Fatal(err)
	}

	if string(body) != "body\n" {
		t.Errorf("the file arrived as %q", body)
	}
}
