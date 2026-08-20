package image_test

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// Unpacking cannot write through a symlink already at the destination.
//
// This one already held - `safePath` refuses it - and the test exists because
// two of its neighbours did not. Placing a cached image into the layer store,
// and copying a tree inside the guest, both followed a planted symlink and
// wrote outside where they were told to; each is fixed and each has its own
// test. Unpack is the most exposed of the three, because what it writes comes
// straight from a registry, so its safety is worth asserting rather than
// assuming.
//
// The trap here is not in the archive - a `..` in a tar entry is the old attack
// and is refused - but in the *destination*, which is a directory this engine
// shares with the guest, where a step can leave a link pointing anywhere.
func TestUnpackingCannotWriteThroughAPlantedSymlink(t *testing.T) {
	t.Parallel()

	dst := t.TempDir()
	outside := t.TempDir()

	err := os.Symlink(outside, filepath.Join(dst, "usr"))
	if err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)

	err = tw.WriteHeader(&tar.Header{Name: "usr/", Typeflag: tar.TypeDir, Mode: 0o755})
	if err != nil {
		t.Fatal(err)
	}

	body := []byte("payload")

	err = tw.WriteHeader(&tar.Header{Name: "usr/tool", Mode: 0o644, Size: int64(len(body))})
	if err != nil {
		t.Fatal(err)
	}

	_, err = tw.Write(body)
	if err != nil {
		t.Fatal(err)
	}

	err = tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Refusing and replacing the link are both right; only escaping is wrong.
	_ = image.Unpack(bytes.NewReader(buf.Bytes()), dst)

	_, err = os.Stat(filepath.Join(outside, "tool"))
	if err == nil {
		t.Error("the archive was written through a symlink, outside the destination")
	}
}
