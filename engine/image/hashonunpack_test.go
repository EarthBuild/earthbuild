package image_test

import (
	"archive/tar"
	"bytes"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// oneFileTar is the smallest archive that can be hashed or not hashed.
func oneFileTar(t *testing.T) []byte {
	t.Helper()

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)
	body := []byte("content")

	err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: "f", Mode: 0o644, Size: int64(len(body)),
	})
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

	return buf.Bytes()
}

// TestWhoHashesALayerIsTheCallersChoice.
//
// **The two callers want opposite things, and both are measured.** Hashing on
// the way in is serial inside the one goroutine handling a layer and runs at
// 330 MB/s on entries the size a layer actually holds; the read-back it saves
// hashes the same bytes across every core. In the guest, where the unpack now
// happens, letting the store read back is 8% faster on a cold FROM. On the host
// it is a wash (E682, E653).
//
// So the choice belongs at the call site rather than in an environment
// variable, and the variable becomes what it should always have been: an
// override for measuring, able to force either way rather than only one.
//
// Identity does not depend on the choice - a supplied digest and a read file
// give the same name, which engine/layer asserts directly - so this is a
// question of who does the work and never of what the answer is.
func TestWhoHashesALayerIsTheCallersChoice(t *testing.T) {
	blob := oneFileTar(t)

	t.Run("the caller that wants them", func(t *testing.T) {
		got, err := image.UnpackApart(bytes.NewReader(blob), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}

		if len(got.Digests) != 1 {
			t.Errorf("unpacked %d digests, want 1", len(got.Digests))
		}
	})

	t.Run("the caller that does not", func(t *testing.T) {
		got, err := image.UnpackApartUnhashed(bytes.NewReader(blob), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}

		if got.Digests != nil {
			t.Errorf("unpacked %d digests, want none - the store reads them back",
				len(got.Digests))
		}

		// Ownership is not recoverable from the tree, so it is handed on
		// whatever is decided about hashing.
		if got.Owners == nil {
			t.Error("an unhashed unpack reported no ownership at all," +
				"\n  which no walk of the finished tree can recover (A2)")
		}
	})

	t.Run("forced off", func(t *testing.T) {
		t.Setenv(image.EnvHashOnUnpack, "0")

		got, err := image.UnpackApart(bytes.NewReader(blob), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}

		if got.Digests != nil {
			t.Error("the override did not stop the caller that asks for digests")
		}
	})

	t.Run("forced on", func(t *testing.T) {
		t.Setenv(image.EnvHashOnUnpack, "1")

		got, err := image.UnpackApartUnhashed(bytes.NewReader(blob), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}

		if len(got.Digests) != 1 {
			t.Error("the override cannot force hashing on, so the arm it turns" +
				"\n  off can never be measured against the arm it turns on")
		}
	})
}
