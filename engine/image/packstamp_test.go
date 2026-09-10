package image

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A context arrives with the time each file deserves, rather than one fixed
// moment for all of them.
//
// cargo decides what to recompile by comparing a source's mtime against the
// fingerprint in `target/`, and rebuilds what is *strictly newer*. A tree that
// lands all at one instant is a tree it cannot reason about: either everything
// is older than the fingerprint and an edit is ignored, or everything is newer
// and nothing is ever fresh. Both were measured.
func TestPackingCarriesTheTimeEachEntryIsGiven(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	putFile(t, root, "old.rs")
	putFile(t, root, "new.rs")

	older := time.Unix(1_600_000_000, 0).UTC()
	newer := time.Unix(1_700_000_123, 456_789_000).UTC()

	at := func(rel string) time.Time {
		if rel == "new.rs" {
			return newer
		}

		return older
	}

	var buf bytes.Buffer

	_, _, err := PackSelectedAt(root, []string{"new.rs", "old.rs"}, &buf, at)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}

	got := modTimesIn(t, &buf)

	// Nanoseconds and all: the resolution is the point (I8). A stamp rounded to
	// the second puts two edits inside one tick and cargo calls the second one
	// fresh.
	if !got["new.rs"].Equal(newer) {
		t.Errorf("new.rs carries %v, wanted %v", got["new.rs"], newer)
	}

	if !got["old.rs"].Equal(older) {
		t.Errorf("old.rs carries %v, wanted %v", got["old.rs"], older)
	}

	if !got["old.rs"].Before(got["new.rs"]) {
		t.Errorf("the order is the whole of the signal: %v is not before %v", got["old.rs"], got["new.rs"])
	}
}

// The unstamped entry point is unchanged, because every layer digest in every
// existing store was computed with it.
func TestPackingWithoutStampsStillFixesEveryEntryAtTheEpoch(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	putFile(t, root, "a")
	putFile(t, root, "b")

	var buf bytes.Buffer

	_, _, err := PackSelected(root, []string{"a", "b"}, &buf)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}

	for name, at := range modTimesIn(t, &buf) {
		if !at.Equal(epoch) {
			t.Errorf("%s carries %v, wanted the epoch %v", name, at, epoch)
		}
	}
}

func putFile(t *testing.T, root, rel string) {
	t.Helper()

	err := os.WriteFile(filepath.Join(root, rel), []byte(rel), 0o600)
	if err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func modTimesIn(t *testing.T, r io.Reader) map[string]time.Time {
	t.Helper()

	times := map[string]time.Time{}
	tr := tar.NewReader(r)

	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}

		if err != nil {
			t.Fatalf("read the archive: %v", err)
		}

		times[h.Name] = h.ModTime
	}

	return times
}
