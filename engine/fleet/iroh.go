package fleet

import (
	"crypto/ed25519"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"

	"github.com/tmc/go-iroh/key"
)

// maxMessage bounds one control message.
//
// A length prefix a peer chooses is an allocation a peer chooses, which is the
// same rule the decoder applies to a count (E245) - and an assignment is a step,
// not a payload, so a megabyte is far above anything real.
const maxMessage = 1 << 20

// maxBlob bounds one blob, which is a payload and legitimately large.
//
// **Two bounds, because there are two kinds of message.** A megabyte is generous
// for an assignment and absurd for a layer: a 32 MiB layer packs to 33 MB, and
// with one bound guarding both, blob transfer refused to carry a real layer at
// all - and no test found it, because every wire test used a layer small enough
// to fit through the hole meant for control messages (E280).
//
// Still a bound. A length is a number the sender chose, and the answer to "how
// big may a layer be" is not "as big as it says" - it is the size at which this
// engine would rather refuse than allocate, which is what the streaming
// limitation in Fetch is a note about.
const maxBlob = 1 << 33

// replyWith sends one reply, framed.
//
// JSON, where an assignment is canonical - an asymmetry with a reason. C.3
// requires the *assignment* to be canonically serialised because peers have to
// agree what a step is; nothing is keyed on a reply's bytes.
func replyWith(w io.Writer, r Reply) error {
	body, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode a reply: %w", err)
	}

	return WriteMessage(w, body)
}

// WriteMessage frames one message with a length.
//
// A stream is bytes and a message is not, so the boundary has to be written
// down. The same argument as a length-prefixed string one level down, for the
// same reason: without it, two messages and one longer message are the same
// bytes.
func WriteMessage(w io.Writer, body []byte) error {
	return writeFramed(w, body, maxMessage)
}

// WriteBlobMessage frames one blob, which may be far larger than a control
// message. See maxBlob.
func WriteBlobMessage(w io.Writer, body []byte) error {
	return writeFramed(w, body, maxBlob)
}

func writeFramed(w io.Writer, body []byte, limit int) error {
	if len(body) > limit {
		return fmt.Errorf("%w: a message of %d bytes, and %d is the most this"+
			" engine sends", ErrMalformed, len(body), limit)
	}

	var n [8]byte

	binary.BigEndian.PutUint64(n[:], uint64(len(body)))

	_, err := w.Write(append(n[:], body...))
	if err != nil {
		return fmt.Errorf("write a message: %w", err)
	}

	return nil
}

// ReadMessage reads one framed message, refusing a length a peer invented.
func ReadMessage(r io.Reader) ([]byte, error) {
	return readFramed(r, maxMessage)
}

// ReadBlobMessage reads one blob, bounded by maxBlob rather than maxMessage.
func ReadBlobMessage(r io.Reader) ([]byte, error) {
	return readFramed(r, maxBlob)
}

func readFramed(r io.Reader, limit int) ([]byte, error) {
	var n [8]byte

	_, err := io.ReadFull(r, n[:])
	if err != nil {
		return nil, fmt.Errorf("%w: no length: %w", ErrMalformed, err)
	}

	size := binary.BigEndian.Uint64(n[:])
	if size > uint64(limit) {
		return nil, fmt.Errorf("%w: a peer asked this engine to allocate %d"+
			" bytes, and %d is the most it will", ErrMalformed, size, limit)
	}

	body := make([]byte, size)

	_, err = io.ReadFull(r, body)
	if err != nil {
		return nil, fmt.Errorf("%w: %d bytes promised and fewer arrived: %w",
			ErrMalformed, size, err)
	}

	return body, nil
}

// publicKeyOf is an endpoint identifier as an ed25519 public key.
//
// The two are the same thirty-two bytes and different Go types, which is the
// library keeping its own vocabulary. The allowlist is written in terms of
// `ed25519.PublicKey` because C.1 is - "the driver publishes an allowlist of
// worker identities" - and translating here keeps that independent of which
// transport is carrying them.
func publicKeyOf(id key.EndpointID) ed25519.PublicKey {
	b := key.PublicKey(id).Bytes()

	return ed25519.PublicKey(b[:])
}
