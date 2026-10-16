package image_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// TestAGrowingFileIsReadWithoutReadingAhead.
//
// **A layer cannot be unpacked until its blob has landed, and that is 1.1s of a
// cold build spent waiting on nothing** - the largest layer of
// `golang:1.26-alpine` fetches for 1.19s and then unpacks for 1.6s, where the
// two could overlap.
//
// Reading a file somebody else is still writing failed twice for reasons worth
// keeping apart (E683). A cached size gives a premature EOF, which the manifest
// solves - the length is known before the first byte. Readahead then pulls in
// pages the writer has not reached, they are zeros, and a cached zero is worse
// than an EOF because it is silently wrong.
//
// So this never reads past what it has been told is there. The telling is a
// callback rather than a file, because how the writer reports progress is not
// this type's business and a test should not need a second process to say so.
func TestAGrowingFileIsReadWithoutReadingAhead(t *testing.T) {
	t.Parallel()

	const (
		chunk = 4096
		n     = 16
	)

	at := filepath.Join(t.TempDir(), "blob")
	want := bytes.Repeat([]byte("abcdefgh"), chunk*n/8)

	// Its final length from the start, which is what stops the premature EOF.
	f, err := os.Create(at)
	if err != nil {
		t.Fatal(err)
	}

	err = f.Truncate(int64(len(want)))
	if err != nil {
		t.Fatal(err)
	}

	var valid atomic.Int64

	go func() {
		for i := range n {
			_, werr := f.WriteAt(want[i*chunk:(i+1)*chunk], int64(i)*chunk)
			if werr != nil {
				return
			}

			valid.Store(int64((i + 1) * chunk))

			time.Sleep(time.Millisecond)
		}

		_ = f.Close()
	}()

	g, err := image.OpenGrowing(at, int64(len(want)), func(have int64) (int64, error) {
		for {
			if v := valid.Load(); v > have {
				return v, nil
			}

			time.Sleep(200 * time.Microsecond)
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	defer g.Close()

	got, err := io.ReadAll(g)
	if err != nil {
		t.Fatalf("reading a growing file: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("read %d bytes of %d, and they differ"+
			"\n  a reader that gets ahead of the writer reads zeros, and a zero"+
			"\n  it caches is a zero it keeps", len(got), len(want))
	}
}

// TestAGrowingFileStopsWhenTheWriterGivesUp.
//
// The writer is a fetch over a network and it can fail. Waiting for a byte that
// is never coming is the worst outcome available - a build that hangs with
// nothing to say - so the reader takes an error from the same callback that
// reports progress, and returns it rather than waiting.
func TestAGrowingFileStopsWhenTheWriterGivesUp(t *testing.T) {
	t.Parallel()

	at := filepath.Join(t.TempDir(), "blob")

	f, err := os.Create(at)
	if err != nil {
		t.Fatal(err)
	}

	err = f.Truncate(1 << 20)
	if err != nil {
		t.Fatal(err)
	}

	_ = f.Close()

	stopped := errors.New("the fetch failed")

	g, err := image.OpenGrowing(at, 1<<20, func(int64) (int64, error) {
		return 0, stopped
	})
	if err != nil {
		t.Fatal(err)
	}

	defer g.Close()

	_, err = io.ReadAll(g)
	if !errors.Is(err, stopped) {
		t.Errorf("reading a file whose writer failed gave %v, want the writer's error"+
			"\n  a reader that waits instead is a build that hangs with nothing to say", err)
	}
}
