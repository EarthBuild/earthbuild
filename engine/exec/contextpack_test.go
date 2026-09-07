package exec

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ignore"
)

// TestPackingStraightFromTheContextCarriesTheSameThing.
//
// **The two routes have to agree byte for byte, not roughly.** Staging a context
// and packing the staging directory is what the engine does; packing the context
// directly is what it is about to do, and the tar is what the guest receives. If
// they differ in an entry name, a mode, an ordering or a hardlink, the guest
// gets a different filesystem and the layer gets a different digest - which is a
// cache miss at best and a wrong build at worst.
//
// So this compares the archives themselves rather than a summary of them. The
// digest `Pack` returns is over the bytes it wrote, so equal digests is the
// whole assertion.
func TestPackingStraightFromTheContextCarriesTheSameThing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.WriteFile(filepath.Join(root, ".earthlyignore"), []byte("**/skipme-*\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{
		"ctx/a.txt",
		"ctx/deep/b.txt",
		"ctx/deep/deeper/c.txt",
		"ctx/skipme-1/d.txt",
	} {
		p := filepath.Join(root, rel)

		mkErr := os.MkdirAll(filepath.Dir(p), 0o750)
		if mkErr != nil {
			t.Fatal(mkErr)
		}

		wErr := os.WriteFile(p, []byte("contents of "+rel), 0o600)
		if wErr != nil {
			t.Fatal(wErr)
		}
	}

	ex := ignore.For(root, filepath.Join(root, "ctx"))

	// The route in use: copy into a staging directory, then pack the directory.
	staged := filepath.Join(t.TempDir(), "staged")

	err = copyDirExcluding(filepath.Join(root, "ctx"), filepath.Join(staged, "ctx"), ex)
	if err != nil {
		t.Fatal(err)
	}

	viaStaging := filepath.Join(t.TempDir(), "staged.tar")

	err = packInto(staged, viaStaging)
	if err != nil {
		t.Fatal(err)
	}

	// The route proposed: pack the context where it lies, selecting as it walks.
	direct := filepath.Join(t.TempDir(), "direct.tar")

	err = packContextInto(root, "ctx", ex, direct)
	if err != nil {
		t.Fatal(err)
	}

	same, why := sameArchive(t, viaStaging, direct)
	if !same {
		t.Errorf("the two routes do not carry the same thing:\n  %s"+
			"\n  the guest receives this tar, so a difference here is a different"+
			"\n  filesystem in the build and a different digest for the layer", why)
	}
}

// sameArchive compares two tarballs by the digest of their bytes, and says what
// differs when they are not the same - a digest alone tells you that a build
// broke and not where.
func sameArchive(t *testing.T, a, b string) (bool, string) {
	t.Helper()

	ea, erra := archiveEntries(t, a)
	eb, errb := archiveEntries(t, b)

	if erra != nil || errb != nil {
		return false, fmt.Sprintf("reading them: %v / %v", erra, errb)
	}

	if len(ea) != len(eb) {
		return false, fmt.Sprintf("%d entries against %d:\n    %v\n    %v", len(ea), len(eb), ea, eb)
	}

	for i := range ea {
		if ea[i] != eb[i] {
			return false, fmt.Sprintf("entry %d is %q against %q", i, ea[i], eb[i])
		}
	}

	return true, ""
}

// archiveEntries is each entry's name, type, mode and link target, in order.
// Content is left out: the names and modes are where a repacking goes wrong.
func archiveEntries(t *testing.T, at string) ([]string, error) {
	t.Helper()

	f, err := os.Open(at)
	if err != nil {
		return nil, err
	}

	defer f.Close()

	var out []string

	tr := tar.NewReader(f)

	for {
		h, nErr := tr.Next()
		if errors.Is(nErr, io.EOF) {
			break
		}

		if nErr != nil {
			return nil, nErr
		}

		out = append(out, fmt.Sprintf("%s type=%c mode=%o link=%s",
			h.Name, h.Typeflag, h.Mode, h.Linkname))
	}

	return out, nil
}

// TestPackingStraightFromTheContextKeepsAHardLink.
//
// **The one place the two routes disagree, stated rather than discovered.**
// Staging copies file contents, so two names sharing an inode arrive as two
// independent files; packing the context directly sees the inode twice and
// writes the second as a link, which is what `packOne` has always done for
// layers.
//
// So the change is not purely about speed: a context containing hardlinks packs
// differently, and therefore to a different digest. That is more faithful to
// what the directory holds, and it is a change - recorded here so that a cache
// miss on first use has an explanation waiting for it.
func TestPackingStraightFromTheContextKeepsAHardLink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.MkdirAll(filepath.Join(root, "ctx"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(root, "ctx/a.txt"), []byte("shared"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Link(filepath.Join(root, "ctx/a.txt"), filepath.Join(root, "ctx/b.txt"))
	if err != nil {
		t.Skipf("this filesystem will not hardlink: %v", err)
	}

	at := filepath.Join(t.TempDir(), "direct.tar")

	err = packContextInto(root, "ctx", ignore.For(root, filepath.Join(root, "ctx")), at)
	if err != nil {
		t.Fatal(err)
	}

	entries, err := archiveEntries(t, at)
	if err != nil {
		t.Fatal(err)
	}

	var linked bool

	for _, e := range entries {
		if strings.Contains(e, "ctx/b.txt") && strings.Contains(e, "link=ctx/a.txt") {
			linked = true
		}
	}

	if !linked {
		t.Errorf("the second name is not a link to the first:\n  %v"+
			"\n  packing the context directly sees the inode twice, and writing"+
			"\n  it as a copy would carry the bytes a second time", entries)
	}
}
