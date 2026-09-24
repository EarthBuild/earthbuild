package bulk_test

import (
	"bytes"
	"crypto/rand"
	"io"
	"net"
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
	got := receive(t, dir, func(w io.ReadWriter) {
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
	got := receive(t, dir, func(w io.ReadWriter) {
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

	host, guestSide := net.Pipe()
	got := make(chan error, 1)

	go func() {
		_, err := bulk.ReceiveBlobs(guestSide, t.TempDir())
		got <- err

		_ = guestSide.Close()
	}()

	// The send fails too - the receiver closes rather than acknowledging - but
	// the receiver's refusal is the one under test.
	_ = bulk.SendBlob(host, "../escaped", strings.NewReader("x"), 1)

	if err := <-got; err == nil {
		t.Fatal("a blob wrote outside the directory it was given")
	}
}

// receive runs send against a live receiver and returns how many blobs landed.
//
// **A pipe rather than a buffer**, because the channel is now bidirectional:
// the receiver acknowledges each blob and the sender waits for it, so the two
// have to run at once. A buffer would deadlock on the first acknowledgement,
// which is the shape of the bug this acknowledgement exists to fix.
func receive(t *testing.T, dir string, send func(io.ReadWriter)) int {
	t.Helper()

	host, guestSide := net.Pipe()

	done := make(chan struct{})

	var (
		n     int
		rxErr error
	)

	go func() {
		n, rxErr = bulk.ReceiveBlobs(guestSide, dir)

		close(done)
	}()

	send(host)

	_ = host.Close()
	<-done

	if rxErr != nil && rxErr != io.EOF {
		t.Fatalf("receive: %v", rxErr)
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

// **The sender does not return until the blob is readable by name.**
//
// Closing the channel is not a barrier: the receiver learns the blob is
// complete by reading end-of-stream, and renames it into place after that. A
// sender that returned at `Close` would hand its caller a path and race the
// rename - which is what happened, and produced a guest reporting `no such
// file` for a blob whose arrival it had just logged.
func TestTheSenderWaitsForTheBlobToBeInPlace(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	host, guestSide := net.Pipe()

	go func() {
		_, _ = bulk.ReceiveBlobs(guestSide, dir)
		_ = guestSide.Close()
	}()

	body := strings.Repeat("x", 1<<16)

	err := bulk.SendBlob(host, "sha256-abc", strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}

	// The instant Send returns, and with no sleep: the point is that waiting is
	// unnecessary, so a test that waited would pass against the bug.
	if _, err := os.Stat(filepath.Join(dir, "sha256-abc")); err != nil {
		t.Errorf("the sender returned before the blob was in place: %v", err)
	}
}
