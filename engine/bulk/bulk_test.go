package bulk_test

import (
	"bytes"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/bulk"
)

// A blob sent on the bulk channel arrives whole and under its own name.
func TestABlobArrivesWhole(t *testing.T) {
	t.Parallel()

	want := make([]byte, 3<<20) // larger than one read, so the loop is exercised
	if _, err := rand.Read(want); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	got := receive(t, dir, func(w io.Writer) {
		if err := bulk.SendBlob(w, "sha256-abc", bytes.NewReader(want), int64(len(want))); err != nil {
			t.Errorf("send: %v", err)
		}
	})

	if got != 1 {
		t.Fatalf("%d blobs arrived, wanted 1", got)
	}

	have, err := os.ReadFile(filepath.Join(dir, "sha256-abc"))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(have, want) {
		t.Errorf("the blob arrived as %d bytes of a wanted %d", len(have), len(want))
	}
}

// Several on one connection, because a build fetches an image's layers at once
// and opening a channel per layer would serialise them.
func TestSeveralBlobsShareTheChannel(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	got := receive(t, dir, func(w io.Writer) {
		for _, name := range []string{"a", "b", "c"} {
			body := strings.Repeat(name, 1000)
			if err := bulk.SendBlob(w, name, strings.NewReader(body), int64(len(body))); err != nil {
				t.Errorf("send %s: %v", name, err)
			}
		}
	})

	if got != 3 {
		t.Errorf("%d blobs arrived, wanted 3", got)
	}
}

// **A truncated blob is a failure, never a short file.** The receiver writes
// what it is given; a sender that dies mid-blob would otherwise leave a file
// the store accepts, unpacks, and turns into a layer missing its tail - a wrong
// build that reports success.
func TestATruncatedBlobIsRefused(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := bulk.SendBlob(&buf, "short", strings.NewReader("only-this"), 1000)
	if err == nil {
		t.Fatal("a blob shorter than its length was sent as if whole")
	}

	if !strings.Contains(err.Error(), "short") {
		t.Errorf("the failure does not name the blob: %v", err)
	}
}

// A name that climbs out of the directory is refused: the sender is the host
// and the receiver is confined, and a confined process that writes where it is
// told is not confined.
func TestANameThatEscapesIsRefused(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	if err := bulk.SendBlob(&buf, "../escaped", strings.NewReader("x"), 1); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	_, err := bulk.ReceiveBlobs(&buf, dir)
	if err == nil {
		t.Fatal("a blob wrote outside the directory it was given")
	}
}

// receive runs send into a pipe and returns how many blobs landed.
func receive(t *testing.T, dir string, send func(io.Writer)) int {
	t.Helper()

	var buf bytes.Buffer

	send(&buf)

	n, err := bulk.ReceiveBlobs(&buf, dir)
	if err != nil && err != io.EOF {
		t.Fatalf("receive: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	// The count and the directory must agree: a receiver that reports more than
	// it wrote is the failure this number exists to make visible.
	if n != len(entries) {
		t.Errorf("%d blobs reported, %d on disk", n, len(entries))
	}

	return len(entries)
}
