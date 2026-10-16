package image

import (
	"fmt"
	"io"
	"os"
)

// Growing reads a file another process is still writing.
//
// **A layer cannot be unpacked until its blob has landed**, which is 1.1s of a
// cold build spent waiting on nothing: the largest layer of
// `golang:1.26-alpine` fetches for 1.19s and then unpacks for 1.6s, and nothing
// about the second depends on the first having finished.
//
// Reading a file somebody else is still writing failed twice, for reasons worth
// keeping apart (E683):
//
//   - **A cached size gives a premature EOF.** The reader stops because the file
//     appears to end, not because the bytes are unavailable. The manifest states
//     the final length before the first byte is fetched, so the file is that
//     length from the start and the question never arises.
//   - **Readahead poisons the cache with zeros.** The first read pulls in pages
//     the writer has not reached; they are zeros, they are cached, and a cached
//     zero is worse than an EOF because it is silently wrong. `FADV_RANDOM`
//     turns that off, which is the whole of the fix and the reason for the
//     platform file beside this one.
//
// Neither is a coherence limit, which is what the first look at this concluded
// and what the correction to E683 records.
type Growing struct {
	f    *os.File
	size int64
	// at is how far this has read, valid how far the writer has confirmed.
	// Reading between them is safe; reading past valid is the zeros.
	at, valid int64
	// more blocks until the writer has passed `have`, and reports how far it
	// has got. It returns an error rather than waiting for ever when the writer
	// has given up: a fetch is a network, and a build that hangs with nothing to
	// say is the worst outcome available.
	more func(have int64) (int64, error)
}

// OpenGrowing opens a file of known final length that is still being written.
//
// The caller says how to wait, because how a writer reports progress is not
// this type's business - and a test should not need a second process to say so.
func OpenGrowing(at string, size int64, more func(have int64) (int64, error)) (*Growing, error) {
	f, err := os.Open(at) //nolint:gosec // a path this engine derived
	if err != nil {
		return nil, fmt.Errorf("open the growing blob %s: %w", at, err)
	}

	// Best effort, and it is the whole fix on the platform that has it: without
	// it the first read caches the rest of the file as zeros. A platform without
	// it reads correctly and slowly, because `more` still bounds every read.
	noReadahead(f)

	return &Growing{f: f, size: size, more: more}, nil
}

func (g *Growing) Read(p []byte) (int, error) {
	if g.at >= g.size {
		return 0, io.EOF
	}

	for g.at >= usableEnd(g.valid, g.size) {
		valid, err := g.more(g.valid)
		if err != nil {
			return 0, err
		}

		// A writer that reports no progress and no error would spin here, so
		// take it at its word only when it has moved.
		if valid > g.valid {
			g.valid = valid
		}

		if g.valid > g.size {
			g.valid = g.size
		}
	}

	if room := usableEnd(g.valid, g.size) - g.at; int64(len(p)) > room {
		p = p[:room]
	}

	n, err := g.f.ReadAt(p, g.at)
	g.at += int64(n)

	if err == io.EOF && g.at < g.size {
		// The file is its full length, so this is not the end of anything - it
		// is a short read of a region the writer has confirmed, which should not
		// happen and must not be reported as completion.
		return n, fmt.Errorf("the blob ended at %d of %d bytes, short of what its"+
			" writer reported as written", g.at, g.size)
	}

	if err != nil && err != io.EOF {
		return n, fmt.Errorf("read the growing blob at %d: %w", g.at, err)
	}

	return n, nil
}

// Close releases the file. The writer is somebody else's and is not touched.
func (g *Growing) Close() error { return g.f.Close() }

// readPage is the granularity a read actually happens at.
//
// 4096 on every platform this runs on. Asking the kernel would be more correct
// and less useful: a page larger than this rounds down to a multiple of itself
// anyway, and one smaller does not exist here.
const readPage = 4096

// usableEnd is how far a reader may go given how far the writer has got.
//
// **The page is the unit, not the byte.** A read touching a page pulls the whole
// page into the cache; if the writer has filled only part of it the rest is
// zeros, and those zeros are cached and handed back when the real bytes arrive.
// That is E683's failure at a finer grain and a worse one - it corrupts the
// middle of a layer rather than stopping the read, and it did:
// `archive/tar: invalid tar header`, on the second cold build and not the
// first, because it depends where the writer's announcements happen to fall.
//
// The end is the exception. A blob's last page is short by definition, and the
// only announcement that reaches the final byte is the one the digest releases -
// so "all of it is there" is the one claim that can be taken at face value.
func usableEnd(valid, size int64) int64 {
	if valid >= size {
		return size
	}

	return valid &^ (readPage - 1)
}
