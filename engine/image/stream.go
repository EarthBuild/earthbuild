package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/timing"
)

// streamLayerApart unpacks one layer as its bytes arrive, reporting whether it
// carries deletion markers.
//
// **Only worth doing with the layers apart.** Buffered, a layer's fetch and its
// own unpack are serial, and the byte budget hides that by unpacking some
// *other* layer meanwhile - which is why E647 measured streaming the merged
// path at 3%, indistinguishable from noise. Apart, the dominant layer is the
// whole critical path and there is nothing else left to unpack while it lands:
// on `python:3.13-slim` the model arrival(largest) + unpack(largest) predicted
// the measured wall to within 14ms.
//
// The digest is checked *after* the unpack, because that is the only place it
// can be. Sound only because the layer goes into a directory of its own that
// the caller discards on failure - the same protection the buffered path relies
// on, stated here because here it is load-bearing rather than incidental.
func streamLayerApart(ctx context.Context, client *http.Client, tok, base string,
	d descriptor, dir string, retain func(string) (io.WriteCloser, error),
) (Unpacked, error) {
	limit := d.Size
	if limit <= 0 {
		limit = 1 << 30
	}

	end := timing.Phase("layer:stream", d.Digest)
	defer end()

	body, err := getStream(ctx, client, tok, base+"/blobs/"+d.Digest, limit)
	if err != nil {
		return Unpacked{}, err
	}

	defer body.Close()

	// Hashed on the way past, so the bytes are read once. A second pass would
	// give back the wall-clock this exists to save.
	hasher := sha256.New()

	// And kept, when somebody asked for them. Same argument: the bytes go past
	// once, and a blob fetched again to be filed would be the fetch this whole
	// path exists to do only once.
	var sink io.Writer = hasher

	if retain != nil {
		kept, retErr := retain(d.Digest)
		if retErr == nil {
			defer kept.Close()

			sink = io.MultiWriter(hasher, kept)
		}
	}

	zr, err := DecompressFrom(io.TeeReader(body, sink), d.MediaType)
	if err != nil {
		return Unpacked{}, err
	}

	defer zr.Close()

	got, unpackErr := UnpackApart(zr, dir)

	// **The rest of the blob still has to be hashed, even after a failure.** A
	// tar reader stops at the end-of-archive marker and leaves the padding
	// unread, so the sum would otherwise be over a prefix and match nothing.
	//
	// And a substituted layer usually fails *inside* the unpacker first,
	// because the body is cut off at the size the manifest declared - so the
	// symptom is "unexpected EOF" and the cause is a blob that is not the one
	// asked for. Hashing anyway lets the mismatch be named as the cause rather
	// than reported as a corrupt archive.
	_, drainErr := io.Copy(io.Discard, io.TeeReader(body, sink))

	sum := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	if sum != d.Digest {
		return got, digestMismatch(d.Digest, sum, dir, unpackErr)
	}

	if unpackErr != nil {
		return got, unpackErr
	}

	if drainErr != nil {
		return got, fmt.Errorf("read the rest of layer %s: %w", d.Digest, drainErr)
	}

	return got, nil
}

// digestMismatch says what was asked for, what arrived, and - when the unpack
// failed first - that the failure is a symptom of the substitution rather than
// a separate problem to chase.
func digestMismatch(want, got, dir string, unpackErr error) error {
	because := ""
	if unpackErr != nil {
		because = fmt.Sprintf("\n  the unpack failed first, which is the symptom"+
			" and not the cause: %v", unpackErr)
	}

	return fmt.Errorf(
		"layer digest mismatch: the manifest asked for %s and the registry"+
			" served %s%s\n  the layer was unpacked into %s, which the caller"+
			" discards; nothing from it is kept", want, got, because, dir)
}

// getStream is get without collecting the body, for a caller that reads it.
//
// The limit is enforced the same way: a registry that keeps sending must not be
// able to fill the disk, and the reader sees a short body rather than a lie.
func getStream(ctx context.Context, client *http.Client, tok, url string, limit int64,
) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	req.Header.Set("Accept", strings.Join(accepts, ", "))

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", url, err)
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()

		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}

	return readCloser{Reader: io.LimitReader(resp.Body, limit), Closer: resp.Body}, nil
}

// readCloser reads from one thing and closes another, so the limit applies to
// what is read while the connection is still released.
type readCloser struct {
	io.Reader
	io.Closer
}
