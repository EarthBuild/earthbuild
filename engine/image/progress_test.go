package image_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// TestABlobSaysHowFarItHasGot.
//
// **The guest unpacks a blob the host is still fetching**, and it must never
// read past what has been written - pages beyond the writer are zeros, and a
// cached zero is a zero kept (E683). So the writer says how far it has got, in
// a file beside the blob, and the reader believes only that.
//
// Staleness here costs latency and never correctness: a marker that lags means
// the guest waits, never that it reads a byte that is not yet there.
//
// A fetch is a network and it can fail. A reader waiting for a byte that is
// never coming is the worst outcome available - a build that hangs with nothing
// to say - so failure travels the same way as progress.
func TestABlobSaysHowFarItHasGot(t *testing.T) {
	t.Parallel()

	blob := filepath.Join(t.TempDir(), "sha256-abc")

	// Nothing said yet is not an error: the fetch may not have started.
	n, failed, err := image.ReadProgress(blob)
	if err != nil || failed != nil || n != 0 {
		t.Fatalf("an unstarted blob reads as (%d, %v, %v), want (0, nil, nil)", n, failed, err)
	}

	err = image.WriteProgress(blob, 4096)
	if err != nil {
		t.Fatal(err)
	}

	n, failed, err = image.ReadProgress(blob)
	if err != nil {
		t.Fatal(err)
	}

	if failed != nil {
		t.Fatalf("a blob in progress reports failure: %v", failed)
	}

	if n != 4096 {
		t.Errorf("progress read back as %d, want 4096", n)
	}

	// And a failure is not a number, so it cannot be mistaken for one.
	err = image.WriteProgressFailure(blob, errors.New("the registry hung up"))
	if err != nil {
		t.Fatal(err)
	}

	n, failed, err = image.ReadProgress(blob)
	if err != nil {
		t.Fatal(err)
	}

	if failed == nil {
		t.Fatal("a failed fetch reads as progress, so a reader waits for bytes" +
			"\n  that are never coming")
	}

	if n != 0 {
		t.Errorf("a failed fetch also reported %d bytes of progress", n)
	}

	if got := failed.Error(); got == "" || !strings.Contains(got, "hung up") {
		t.Errorf("the failure came back as %q, losing what went wrong", got)
	}
}
