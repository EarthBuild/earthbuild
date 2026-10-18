package image_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// A pull can keep each layer in a directory of its own.
//
// **The merge is what makes unpacking serial.** Layers go into one directory
// oldest first, so a later one may overwrite what an earlier one wrote and the
// order cannot be given up (E641). Nothing about *fetching* or *unpacking* a
// layer depends on another layer, though - only the merge does - so a puller
// that keeps them apart can do all of it at once, and the assembling becomes a
// mount, which is what overlayfs is for.
//
// The digests come back in the order they must be stacked, which is the order
// the manifest lists them.
func TestAPullCanKeepLayersApart(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{layers: [][]byte{
		gzipTar(t, "oldest", "one"),
		gzipTar(t, "middle", "two"),
		gzipTar(t, "newest", "three"),
	}}

	host := reg.start(t)
	dir := t.TempDir()

	got, _, err := image.PullApart(context.Background(), host+"/library/test:1", dir,
		image.Options{Plain: true})
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 3 {
		t.Fatalf("the pull produced %d layers, want 3", len(got))
	}

	// Each layer's own directory holds only that layer's file.
	for i, want := range []string{"oldest", "middle", "newest"} {
		at := filepath.Join(dir, got[i].Dir)

		entries, rerr := os.ReadDir(at)
		if rerr != nil {
			t.Fatalf("layer %d has no directory: %v", i, rerr)
		}

		if len(entries) != 1 || entries[0].Name() != want {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}

			t.Errorf("layer %d holds %v, want just %q"+
				"\n  layers kept apart must not be merged into one another", i, names, want)
		}
	}
}

// And the layers come back in the order they must be stacked.
func TestLayersComeBackOldestFirst(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{layers: [][]byte{
		gzipTar(t, "shared", "from the older layer"),
		gzipTar(t, "shared", "from the newer layer"),
	}}

	host := reg.start(t)
	dir := t.TempDir()

	got, _, err := image.PullApart(context.Background(), host+"/library/test:1", dir,
		image.Options{Plain: true})
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 {
		t.Fatalf("the pull produced %d layers, want 2", len(got))
	}

	b, err := os.ReadFile(filepath.Join(dir, got[1].Dir, "shared"))
	if err != nil {
		t.Fatal(err)
	}

	if string(b) != "from the newer layer" {
		t.Errorf("the last layer holds %q, want the newer content"+
			"\n  the order returned is the order they stack, oldest first", b)
	}
}

// TestALayerKeptApartKeepsItsWhiteouts is the correctness condition that makes
// per-layer storage a different thing from a merged unpack, not merely a faster
// one.
//
// **A whiteout is a deletion of something in a *lower* layer.** Unpacking an
// image into one tree can therefore apply it as a deletion the moment it is
// read: the lower layer is already in that tree. Kept apart there is nothing
// below to delete - the entry names a file this layer does not have - so
// applying it removes nothing, the marker is dropped, and the file it was meant
// to delete survives into the stack.
//
// So the marker has to be preserved, literally, and turned into an overlayfs
// whiteout when the layer is stacked - which is what the materialiser's
// translation step already exists to do, and what `.unmarked` records the
// absence of.
func TestALayerKeptApartKeepsItsWhiteouts(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{layers: [][]byte{
		gzipTar(t, "gone", "here in the base"),
		gzipTar(t, ".wh.gone", ""),
	}}

	host := reg.start(t)
	dir := t.TempDir()

	got, _, err := image.PullApart(context.Background(), host+"/library/test:1", dir,
		image.Options{Plain: true})
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 {
		t.Fatalf("the pull produced %d layers, want 2", len(got))
	}

	// The base still has the file: nothing about the layer above it may reach in.
	_, err = os.Stat(filepath.Join(dir, got[0].Dir, "gone"))
	if err != nil {
		t.Errorf("the base layer lost its own file: %v", err)
	}

	// **And the pull says which layers carry one, because it just read them.**
	// The materialiser otherwise walks every layer to find out - 1.44s of a cold
	// `golang:1.26-alpine` pull, against an unpacker that had the answer and
	// threw it away.
	if got[0].Marked {
		t.Error("the base layer has no markers and must not be reported as marked")
	}

	if !got[1].Marked {
		t.Error("the layer carrying .wh.gone must be reported as marked")
	}

	// And the layer above carries the marker, so the stack can act on it.
	marker := filepath.Join(dir, got[1].Dir, ".wh.gone")

	_, err = os.Stat(marker)
	if err != nil {
		entries, _ := os.ReadDir(filepath.Join(dir, got[1].Dir))

		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}

		t.Fatalf("the whiteout was dropped: %v\n"+
			"  the layer holds %v\n"+
			"  a deletion applied to a layer that has nothing to delete is a deletion lost,\n"+
			"  and the file it named survives in the stack below", err, names)
	}
}

// TestAPullCanKeepTheCompressedLayersItFetched.
//
// **A blob is 61MB where its tree is 228MB and 15034 files**, and a layer kept
// as a blob can still be named and served: E656 names one from its archive and
// E657 packs part of one, both byte-for-byte with the unpacked tree. E658
// measured the pair at 76% of an unpack-and-name.
//
// None of which is reachable if the pull throws the bytes away the moment they
// are unpacked, which is what it did. The retention is the caller's - `ir`
// imports this package, so this package cannot name `engine/blob` - and it is a
// writer per layer rather than a buffer, because the streaming path never holds
// a whole one.
func TestAPullCanKeepTheCompressedLayersItFetched(t *testing.T) {
	t.Parallel()

	for _, streaming := range []bool{false, true} {
		reg := &fakeRegistry{layers: [][]byte{
			gzipTar(t, "oldest", "one"),
			gzipTar(t, "newest", "two"),
		}}

		host := reg.start(t)

		kept := map[string]*keptBlob{}

		var mu sync.Mutex

		got, _, err := image.PullApart(context.Background(), host+"/library/test:1", t.TempDir(),
			image.Options{
				Plain: true, Stream: streaming,
				Retain: func(digest string) (io.WriteCloser, error) {
					mu.Lock()
					defer mu.Unlock()

					b := &keptBlob{}
					kept[digest] = b

					return b, nil
				},
			})
		if err != nil {
			t.Fatalf("stream=%v: %v", streaming, err)
		}

		if len(kept) != len(got) {
			t.Fatalf("stream=%v: the pull produced %d layers and kept %d",
				streaming, len(got), len(kept))
		}

		for _, l := range got {
			b, ok := kept[l.Digest]
			if !ok {
				t.Fatalf("stream=%v: layer %s was not kept", streaming, l.Digest)
			}

			if !b.closed {
				t.Errorf("stream=%v: %s was left open, so a caller filing it"+
					" cannot know it is complete", streaming, l.Digest)
			}

			// The bytes are the ones the manifest named, which is the only
			// thing that makes keeping them worth anything.
			if got := "sha256:" + hex.EncodeToString(sha256Of(b.Bytes())); got != l.Digest {
				t.Errorf("stream=%v: kept %s under the name %s",
					streaming, got, l.Digest)
			}
		}
	}
}

// keptBlob is somewhere for a retained layer to go, and a record of whether the
// pull said it was finished.
type keptBlob struct {
	bytes.Buffer

	closed bool
}

func (k *keptBlob) Close() error { k.closed = true; return nil }

func sha256Of(b []byte) []byte {
	sum := sha256.Sum256(b)

	return sum[:]
}

// TestAnImageCanBeFetchedWithoutBeingUnpacked.
//
// **The host stops unpacking when the store moves onto the guest's device.** It
// still has the network, the credentials and the manifest, so fetching stays
// here; unpacking goes to the side that owns the filesystem and can grant what
// an archive declares.
//
// What comes back is enough to ask for that: where each layer's compressed bytes
// are, how they are compressed, and in what order they stack.
func TestAnImageCanBeFetchedWithoutBeingUnpacked(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{layers: [][]byte{
		gzipTar(t, "oldest", "one"),
		gzipTar(t, "middle", "two"),
		gzipTar(t, "newest", "three"),
	}}

	host := reg.start(t)
	dir := t.TempDir()

	got, _, err := image.FetchApart(context.Background(), host+"/library/test:1", dir,
		image.Options{Plain: true})
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 3 {
		t.Fatalf("the fetch produced %d layers, want 3", len(got))
	}

	for i, l := range got {
		if l.MediaType == "" {
			t.Errorf("layer %d says nothing about how it is compressed, so"+
				" nothing can read it", i)
		}

		at := filepath.Join(dir, l.At)

		body, rerr := os.ReadFile(at)
		if rerr != nil {
			t.Fatalf("layer %d was not written: %v", i, rerr)
		}

		// The bytes are the ones the manifest named, which is the whole of what
		// makes handing the path on safe.
		if got := "sha256:" + hex.EncodeToString(sha256Of(body)); got != l.Digest {
			t.Errorf("layer %d holds %s under the name %s", i, got, l.Digest)
		}
	}

	// **Nothing was unpacked.** The directory holds the blobs and no trees; a
	// fetch that quietly unpacked would put fifteen thousand files across a
	// share for nobody.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("the fetch left a directory %q, so something was unpacked", e.Name())
		}
	}
}
