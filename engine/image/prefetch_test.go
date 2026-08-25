package image_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// Layers are fetched while the one before them is being unpacked.
//
// A layer is an independent object: nothing about fetching one depends on
// another having arrived. Unpacking is *not* independent - the layers go into
// one directory in order, and a later one overwrites what an earlier one put
// there - so the order stays, and only the waiting overlaps.
//
// Measured on `golang:1.26-alpine`, five layers: 1.697s of fetching and 3.838s
// of unpacking, and a pull of 5.934s. The sum was the whole, which is what
// strictly serial looks like (E641).
//
// The registry here holds each blob request open, so a fetch that starts while
// another is in flight is the only way the count can exceed one.
func TestALayerIsFetchedWhileTheOneBeforeItUnpacks(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{
		layers: [][]byte{
			gzipTar(t, "a", "one"),
			gzipTar(t, "b", "two"),
			gzipTar(t, "c", "three"),
			gzipTar(t, "d", "four"),
		},
		blobDelay: 40 * time.Millisecond,
	}

	host := reg.start(t)
	dir := t.TempDir()

	_, err := image.Pull(context.Background(), host+"/library/test:1", dir,
		image.Options{Plain: true})
	if err != nil {
		t.Fatal(err)
	}

	if got := reg.peakBlobs(); got < 2 {
		t.Errorf("at most %d blob request was ever in flight, want more than one"+
			"\n  the layers are fetched one after another, so the whole of a pull"+
			" is spent waiting for the next one to arrive", got)
	}

	// And the result is still the image: order preserved, every layer applied.
	for name, want := range map[string]string{
		"a": "one", "b": "two", "c": "three", "d": "four",
	} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("layer %s did not land: %v", name, err)
		}

		if string(b) != want {
			t.Errorf("%s holds %q, want %q", name, b, want)
		}
	}
}

// A later layer's content wins, which is why unpacking stays in order.
//
// **Checked rather than assumed.** Overlapping the *fetches* is only safe
// because the ordering requirement belongs to unpacking alone, and that
// requirement had been asserted from the shape of the code - one directory,
// `Unpack(r, dir)` - rather than demonstrated. Two layers writing the same path
// demonstrate it: applied oldest-first the newer content survives, so a pull
// that let the unpacking race would produce whichever layer happened to finish
// last, and the image would differ run to run for no reason a key could see.
func TestALaterLayerWinsThePathItShares(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{
		layers: [][]byte{
			gzipTar(t, "shared", "from the older layer"),
			gzipTar(t, "shared", "from the newer layer"),
		},
		blobDelay: 30 * time.Millisecond,
	}

	host := reg.start(t)
	dir := t.TempDir()

	_, err := image.Pull(context.Background(), host+"/library/test:1", dir,
		image.Options{Plain: true})
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(dir, "shared"))
	if err != nil {
		t.Fatal(err)
	}

	if got := string(b); got != "from the newer layer" {
		t.Errorf("the shared path holds %q, want the newer layer's content"+
			"\n  layers are applied oldest first, and a pull that unpacked them"+
			" out of order would keep whichever finished last", got)
	}
}
