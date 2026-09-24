package image_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// TestAFetchAnnouncesEachLayerBeforeItLands.
//
// **The guest unpacks; the host fetches.** With the store on the guest's device
// the two are on opposite sides of a VM boundary, and a guest told about a layer
// only once it has landed makes the fetch and the unpack serial - 1.19s and then
// 1.6s for the layer that is the critical path, where nothing about the second
// depends on the first having finished.
//
// So a layer is announced when its file exists at its final length, before any
// of it is there. `Stream` is the same overlap for an unpack done here; this is
// it for one done somewhere else.
func TestAFetchAnnouncesEachLayerBeforeItLands(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{layers: [][]byte{
		gzipTar(t, "oldest", "one"),
		gzipTar(t, "newest", "two"),
	}}

	host := reg.start(t)
	dir := t.TempDir()

	var (
		mu       sync.Mutex
		order    []string
		announce []image.FetchedLayer
	)

	got, _, err := image.FetchApart(context.Background(), host+"/library/test:1", dir,
		image.Options{
			Plain: true,
			Fetching: func(_ int, l image.FetchedLayer) {
				mu.Lock()
				defer mu.Unlock()

				order = append(order, "fetching:"+l.Digest)
				announce = append(announce, l)
			},
			Fetched: func(_ int, l image.FetchedLayer) {
				mu.Lock()
				defer mu.Unlock()

				order = append(order, "fetched:"+l.Digest)
			},
		})
	if err != nil {
		t.Fatal(err)
	}

	if len(announce) != len(got) {
		t.Fatalf("%d layers were fetched and %d announced early", len(got), len(announce))
	}

	// Every announcement comes before every landing: the guest must be able to
	// start on the last layer before the first has finished arriving.
	for i, step := range order {
		if strings.HasPrefix(step, "fetched:") && i < len(announce) {
			t.Errorf("a layer landed at step %d, before all %d had been announced"+
				"\n  %v", i, len(announce), order)

			break
		}
	}

	for _, l := range announce {
		if l.Size <= 0 {
			t.Errorf("layer %s was announced without a size, so a reader cannot"+
				"\n  tell where it ends and must wait for it to land", l.Digest)
		}

		// The file is already its full length, which is what stops a reader
		// stopping at a cached size (E683).
		st, serr := os.Stat(filepath.Join(dir, l.At))
		if serr != nil {
			t.Errorf("layer %s was announced before its file existed: %v", l.Digest, serr)

			continue
		}

		if st.Size() != l.Size {
			t.Errorf("layer %s was announced at %d bytes, and its file is %d",
				l.Digest, l.Size, st.Size())
		}
	}
}

// TestAStreamedFetchNeverAnnouncesABadLayerAsComplete.
//
// **A reader on the far side of a VM cannot be told to discard what it has
// already unpacked.** The host's own streaming unpack is sound because it
// unpacks into a directory it throws away on a bad digest; a guest doing the
// unpacking is not reachable that way.
//
// So the digest gates the last byte. Progress stops one short of the end until
// the bytes verify, and a reader that has taken everything it was offered still
// has an unfinished layer - which it cannot place, which is the whole guarantee.
func TestAStreamedFetchNeverAnnouncesABadLayerAsComplete(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{layers: [][]byte{gzipTar(t, "hello", "world")}}

	// Well-formed bytes that are not the ones asked for: a blob that merely
	// failed to decompress would prove nothing about the digest check.
	reg.serveInstead = gzipTar(t, "hello", "not the layer you asked for")

	host := reg.start(t)
	dir := t.TempDir()

	var announced []image.FetchedLayer

	_, _, err := image.FetchApart(context.Background(), host+"/library/test:1", dir,
		image.Options{
			Plain:    true,
			Fetching: func(_ int, l image.FetchedLayer) { announced = append(announced, l) },
		})
	if err == nil {
		t.Fatal("a layer whose bytes do not match its digest was accepted")
	}

	for _, want := range []string{"digest", "sha256:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q, so a reader cannot tell a"+
				" substituted layer from a network failure:\n  %v", want, err)
		}
	}

	if len(announced) == 0 {
		t.Fatal("nothing was announced, so this proves nothing about what a reader saw")
	}

	for _, l := range announced {
		blob := filepath.Join(dir, l.At)

		n, failed, rerr := image.ReadProgress(blob)
		if rerr != nil {
			t.Fatal(rerr)
		}

		if failed == nil {
			t.Errorf("layer %s reports no failure, so a reader waits for bytes"+
				" that are never coming", l.Digest)
		}

		if n >= l.Size {
			t.Errorf("layer %s was announced complete at %d of %d bytes despite a"+
				"\n  bad digest - a reader could finish it and place it", l.Digest, n, l.Size)
		}
	}
}
