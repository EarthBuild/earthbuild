package image

import (
	"io"
	"os"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// EnvLayerCompression chooses what an image's layers are compressed with.
//
// **`gzip`, the default, because everything reads it.** Measured on the base
// layer of a `rust:slim-bookworm` image: 898 MB packed, 305 MB gzipped, 286 MB
// under zstd - and zstd took 0.94s for the whole 898 MB, so the cost is not the
// consideration either way. What decides it is that a gzipped layer is readable
// by every registry, runtime and `docker load` in existence, and a zstd one is
// not by anything older than a few years.
//
// **`zstd`** is worth asking for where both ends are yours: another 7% off, and
// several times faster to decompress on every pull that follows.
//
// **`none`** writes the tar as it lies. That is what this did before, and it
// moves three times the bytes - which on a Rust workspace whose `target/` runs
// to tens of gigabytes is the difference between publishing a build tree and
// not bothering.
const EnvLayerCompression = "EARTH_LAYER_COMPRESSION"

// layerMediaType names what the blobs were written with, so a puller knows
// what it is holding.
func layerMediaType() string {
	switch os.Getenv(EnvLayerCompression) {
	case "none":
		return ocispec.MediaTypeImageLayer
	case "zstd":
		return ocispec.MediaTypeImageLayerZstd
	default:
		return ocispec.MediaTypeImageLayerGzip
	}
}

// compressorTo wraps a writer in the compressor this image is using.
//
// **Fixed settings, because an image's identity is its bytes.** A compressor
// that varied its level, or stamped a name or a time into its header, would
// make two builds of one input produce two different images - which is the
// property the rest of this file exists to preserve. gzip's header carries an
// optional modification time and this leaves it at zero.
func compressorTo(w io.Writer) io.WriteCloser {
	switch os.Getenv(EnvLayerCompression) {
	case "none":
		return nopCloser{w}

	case "zstd":
		// Errors only for an invalid option, and these are constants.
		z, _ := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedDefault))

		return z

	default:
		return gzip.NewWriter(w)
	}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }
