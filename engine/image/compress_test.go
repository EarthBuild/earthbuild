package image

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// A zstd layer decompresses.
//
// zstd is a registry reality, not a hypothetical: the OCI image spec defines
// `application/vnd.oci.image.layer.v1.tar+zstd`, and a registry that serves one
// serves it to everybody. Refusing it is honest (I10) and still means the image
// cannot be pulled, so the refusal was a placeholder rather than a decision.
//
// The library was already in the module graph as an indirect dependency, so this
// promotes a `// indirect` line rather than adding a supply-chain surface.
func TestAZstdLayerDecompresses(t *testing.T) {
	t.Parallel()

	const body = "the layer's bytes\n"

	var buf bytes.Buffer

	w, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}

	_, err = io.WriteString(w, body)
	if err != nil {
		t.Fatal(err)
	}

	err = w.Close()
	if err != nil {
		t.Fatal(err)
	}

	for _, mt := range []string{
		"application/vnd.oci.image.layer.v1.tar+zstd",
		"application/vnd.docker.image.rootfs.diff.tar.zstd",
	} {
		rc, err := decompress(buf.Bytes(), mt)
		if err != nil {
			t.Errorf("%s: %v", mt, err)

			continue
		}

		got, err := io.ReadAll(rc)
		rc.Close()

		if err != nil {
			t.Errorf("%s: read: %v", mt, err)

			continue
		}

		if string(got) != body {
			t.Errorf("%s: came back as %q", mt, got)
		}
	}
}

// A blob whose bytes disagree with its declared type is refused.
//
// `decompress` chooses by media type and never sniffs, deliberately - the doc
// comment says a blob disagreeing with its type is one to refuse. That has to
// hold for the new arm too: the failure must arrive here, named, rather than
// deep in the unpacker as a complaint about a corrupt archive, which is the
// exact diagnosis the old placeholder existed to avoid.
func TestAMislabelledZstdLayerIsRefused(t *testing.T) {
	t.Parallel()

	_, err := decompress([]byte("this is not zstd"), "application/vnd.oci.image.layer.v1.tar+zstd")
	if err == nil {
		t.Fatal("a blob that is not zstd was accepted as a zstd layer")
	}

	// Not merely "an error": before zstd was handled this refused every zstd
	// blob with `unsupported layer media type "…tar+zstd"`, which contains the
	// word and would have passed a laxer assertion. The claim is that the type
	// was *recognised* and the bytes rejected, so the one message that must not
	// appear is the one meaning "recognised nothing".
	if strings.Contains(err.Error(), "unsupported layer media type") {
		t.Errorf("the type was not recognised at all, so this says nothing"+
			" about mislabelled bytes: %v", err)
	}
}

// An unknown media type is still refused by name.
//
// The point of the default arm is the diagnosis. Losing it while adding zstd
// would trade one unsupported-format bug for a worse error message on every
// other one.
func TestAnUnknownMediaTypeIsNamed(t *testing.T) {
	t.Parallel()

	_, err := decompress([]byte("x"), "application/vnd.oci.image.layer.v1.tar+brotli")
	if err == nil {
		t.Fatal("an unknown layer type was accepted")
	}

	if !bytes.Contains([]byte(err.Error()), []byte("brotli")) {
		t.Errorf("the error does not name the type it could not handle: %v", err)
	}
}
