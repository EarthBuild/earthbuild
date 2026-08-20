package image

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// zstdMagic begins every zstd frame - RFC 8878 §3.1.1, little-endian
// 0xFD2FB528.
var zstdMagic = []byte{0x28, 0xB5, 0x2F, 0xFD}

// decompress wraps a blob according to its media type.
//
// Chosen by media type rather than by sniffing the bytes: a blob whose content
// disagrees with its declared type is a blob to refuse, not one to interpret
// helpfully.
func decompress(blob []byte, mediaType string) (io.ReadCloser, error) {
	switch {
	case strings.HasSuffix(mediaType, ".tar+gzip"), strings.HasSuffix(mediaType, ".tar.gzip"):
		zr, err := gzip.NewReader(bytes.NewReader(blob))
		if err != nil {
			return nil, fmt.Errorf("decompress layer: %w", err)
		}

		return zr, nil

	case strings.HasSuffix(mediaType, ".tar+zstd"), strings.HasSuffix(mediaType, ".tar.zstd"):
		// **zstd.NewReader validates nothing.** gzip.NewReader reads and checks
		// the header eagerly, so the gzip arm above fails here for a blob that
		// is not gzip; the zstd decoder is lazy and defers everything to the
		// first Read, which lands the failure inside the unpacker as a complaint
		// about a corrupt archive - the wrong component, and the exact diagnosis
		// the default arm below exists to avoid.
		//
		// So the frame magic is checked here, which is what gzip does for its
		// own. Parity is then exact in both directions: header at construction,
		// body at read.
		if !bytes.HasPrefix(blob, zstdMagic) {
			return nil, fmt.Errorf(
				"decompress zstd layer: declared %s but the bytes do not begin"+
					" with a zstd frame", mediaType)
		}

		// IOReadCloser, not the decoder: zstd.Decoder's Close returns nothing
		// and so does not satisfy io.ReadCloser, and a decoder that is never
		// closed leaks the goroutines it decodes on.
		zr, err := zstd.NewReader(bytes.NewReader(blob))
		if err != nil {
			return nil, fmt.Errorf("decompress zstd layer: %w", err)
		}

		return zr.IOReadCloser(), nil

	case strings.HasSuffix(mediaType, ".tar"), mediaType == "":
		return io.NopCloser(bytes.NewReader(blob)), nil

	default:
		// Named rather than treated as an uncompressed tar, which would fail
		// deep inside the unpacker with a message about a corrupt archive - a
		// diagnosis pointing at the wrong component entirely.
		return nil, fmt.Errorf("unsupported layer media type %q", mediaType)
	}
}
