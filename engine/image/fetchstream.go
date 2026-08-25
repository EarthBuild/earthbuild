package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/timing"
)

// announceChunk is how often a streaming fetch says how far it has got.
//
// A marker per read would be a rename per 32KiB. A marker per megabyte is one
// wait of about 18ms at the 56 MB/s a single connection manages, which is
// nothing beside the 1.6s unpack it is overlapping.
const announceChunk = 1 << 20

// createSized makes a blob file at its final length before any of it exists.
//
// **The length is why a growing file can be read at all.** A reader stops at a
// cached size, so a file that is already its full length never reports a
// premature end - and the manifest states that length before the first byte is
// fetched (E683).
func createSized(at string, size int64) error {
	f, err := os.OpenFile(at, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // a path this engine derived
	if err != nil {
		return fmt.Errorf("create the blob %s: %w", at, err)
	}

	defer f.Close()

	err = f.Truncate(size)
	if err != nil {
		return fmt.Errorf("reserve %d bytes for the blob %s: %w", size, at, err)
	}

	return nil
}

// streamBlobTo fetches one layer into a file somebody else is already reading.
//
// **The digest gates the last byte.** The bytes are written as they arrive and a
// reader is unpacking them long before they are known to be the right bytes -
// which is what `streamLayerApart` already does on the host's own path, and is
// sound for the same reason: the reader unpacks into a directory it discards.
// What is different here is that the reader is on the other side of a VM
// boundary and cannot be told to discard it after the fact.
//
// So it is never told the layer is complete until the digest matches. It reads
// up to one byte short of the end, waits, and is released only by a marker that
// verification has written - so a reader can no more finish a bad layer than it
// can read a byte that was never fetched, and nothing is placed unverified.
func streamBlobTo(ctx context.Context, client *http.Client, tok, base string,
	d descriptor, at string, watched bool, ledger *Ledger,
) error {
	limit := d.Size
	if limit <= 0 {
		limit = 1 << 30
	}

	end := timing.Phase("layer:stream:guest", d.Digest)
	defer end()

	body, err := getStream(ctx, client, tok, base+"/blobs/"+d.Digest, limit)
	if err != nil {
		failNoted(at, watched, ledger, err)

		return err
	}

	defer body.Close()

	f, err := os.OpenFile(at, os.O_WRONLY, 0o600) //nolint:gosec // a path this engine derived
	if err != nil {
		failNoted(at, watched, ledger, err)

		return fmt.Errorf("open the blob %s to fill it: %w", at, err)
	}

	defer f.Close()

	wrote, err := fillAndHash(body, f, at, d.Size, watched, ledger)
	if err != nil {
		failNoted(at, watched, ledger, err)

		return err
	}

	if wrote.n != d.Size {
		err = fmt.Errorf("layer %s ended after %d bytes, and its manifest says %d",
			d.Digest, wrote.n, d.Size)
		failNoted(at, watched, ledger, err)

		return err
	}

	got := "sha256:" + hex.EncodeToString(wrote.sum)
	if got != d.Digest {
		// The same message the buffered path gives, because a reader telling a
		// substituted layer from a network failure is the whole point of it.
		err = fmt.Errorf("blob does not match its digest\n  expected %s\n  received %s",
			d.Digest, got)
		failNoted(at, watched, ledger, err)

		return err
	}

	// **The release.** Everything before this said "all but the last byte".
	if !watched {
		return nil
	}

	return note(at, ledger, d.Size)
}

// filled is what a stream wrote and what it hashed.
type filled struct {
	n   int64
	sum []byte
}

func fillAndHash(body io.Reader, f *os.File, at string, size int64, watched bool,
	ledger *Ledger,
) (filled, error) {
	h := sha256.New()
	buf := make([]byte, announceChunk)

	var (
		n         int64
		announced int64
	)

	for {
		got, rerr := body.Read(buf)
		if got > 0 {
			h.Write(buf[:got])

			_, werr := f.WriteAt(buf[:got], n)
			if werr != nil {
				return filled{}, fmt.Errorf("write the blob at %d: %w", n, werr)
			}

			n += int64(got)

			// **Whole pages, and one short of the end.** A reader can only use
			// pages the writer has finished (see usableEnd), so announcing a
			// part-page just makes it round down and wait; and announcing the
			// last byte is what says the layer is complete, which only the
			// digest may do.
			if say := min(n&^(readPage-1), size-1); watched && say > announced {
				err := note(at, ledger, say)
				if err != nil {
					return filled{}, err
				}

				announced = say
			}
		}

		if rerr == io.EOF {
			break
		}

		if rerr != nil {
			return filled{}, fmt.Errorf("read the layer's bytes: %w", rerr)
		}
	}

	return filled{n: n, sum: h.Sum(nil)}, nil
}

// streamLayers fills every blob at once, announcing each as it verifies.
//
// **All of them, not a window.** The window the buffered fetch keeps exists to
// bound how many un-unpacked layers are held in *memory*; these are files, so
// there is nothing to bound and no blob is ever held whole.
//
// `Fetched` still arrives in manifest order, because that is what it promises
// and because a caller indexing by position has no way to know otherwise. A
// layer that finishes early waits for its turn; one that fails stops the
// announcements, since everything after it is about to be discarded anyway.
func streamLayers(ctx context.Context, p prepared, layers []descriptor,
	out []FetchedLayer, root, ref string, fetched func(int, FetchedLayer), watched bool,
	ledger *Ledger,
) error {
	var wg sync.WaitGroup

	failed := make([]error, len(layers))
	done := make([]chan struct{}, len(layers))

	for i := range layers {
		done[i] = make(chan struct{})
	}

	for i, d := range layers {
		wg.Go(func() {
			defer close(done[i])

			failed[i] = streamBlobTo(ctx, p.client, p.tok, p.base, d,
				filepath.Join(root, out[i].At), watched, ledger)
		})
	}

	if fetched != nil {
		wg.Go(func() {
			for i := range layers {
				<-done[i]

				if failed[i] != nil {
					return
				}

				fetched(i, out[i])
			}
		})
	}

	wg.Wait()

	for i, err := range failed {
		if err != nil {
			return fmt.Errorf("layer %d of %s: %w", i, ref, err)
		}
	}

	return nil
}

// failNoted tells a waiting reader why it will get no further.
//
// Only when somebody is reading: with nothing streaming there is no reader, and
// a marker beside every blob would be litter written once per fetch for nobody.
func failNoted(at string, watched bool, ledger *Ledger, cause error) {
	if !watched {
		return
	}

	if ledger != nil {
		ledger.Fail(filepath.Base(at), cause)
	}

	_ = WriteProgressFailure(at, cause)
}

// note records progress everywhere a reader might look.
//
// **Both, and that is not belt and braces.** The ledger is the fast answer - a
// file on a shared mount answers about 460ms late, which is the whole of why
// streaming did not pay (E688) - but a guest whose fault-in relay did not come
// up has no socket to ask on and falls back to the file. Writing only the
// ledger left those two halves disagreeing, and the symptom was a build that
// sat for five minutes and then said `context canceled`.
//
// The file costs a rename per megabyte against a fetch of over a second. That
// is not a price worth a silent hang.
func note(at string, ledger *Ledger, n int64) error {
	if ledger != nil {
		ledger.Set(filepath.Base(at), n)
	}

	return WriteProgress(at, n)
}
