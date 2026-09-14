package layer

import (
	"encoding/binary"
	"fmt"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// ChildDigests is the subdirectories a Directory message points at.
//
// **Not a protobuf library, and deliberately.** Walking a tree means asking for
// the root, seeing what it names, and asking for whatever is missing - so what
// a fetch needs from a Directory is its `DirectoryNode` digests and nothing
// else. Everything else in the message is for whoever materialises it, and a
// decoder that read it all would be a second definition of the schema to keep
// in step with the encoder beside it.
//
// Unknown fields are skipped, as protobuf intends: a peer running a later
// version of the API may send fields this does not know, and refusing them
// would make a forward-compatible format backward-breaking. What is refused is
// a length running past the end of the buffer, which is what a truncated or
// corrupt message looks like and is not something to read half of.
func ChildDigests(b []byte) ([]ir.NodeID, error) {
	var out []ir.NodeID

	err := eachField(b, func(field int, wire int, v []byte) error {
		if field != fieldDirectories || wire != wireBytes {
			return nil
		}

		id, err := digestOfNode(v)
		if err != nil {
			return err
		}

		out = append(out, id)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// digestOfNode reads the digest out of one DirectoryNode.
func digestOfNode(b []byte) (ir.NodeID, error) {
	var (
		found bool
		id    ir.NodeID
	)

	err := eachField(b, func(field int, wire int, v []byte) error {
		if field != fieldDigest || wire != wireBytes {
			return nil
		}

		hex, err := hashOfDigest(v)
		if err != nil {
			return err
		}

		parsed, err := ir.ParseNodeID(hex)
		if err != nil {
			return fmt.Errorf("a DirectoryNode names %q, which is not a digest: %w", hex, err)
		}

		id, found = parsed, true

		return nil
	})
	if err != nil {
		return ir.NodeID{}, err
	}

	if !found {
		return ir.NodeID{}, fmt.Errorf("a DirectoryNode carries no digest, so nothing can be asked for")
	}

	return id, nil
}

// hashOfDigest reads the hex string out of one Digest.
func hashOfDigest(b []byte) (string, error) {
	var hex string

	err := eachField(b, func(field int, wire int, v []byte) error {
		if field == fieldDigestHash && wire == wireBytes {
			hex = string(v)
		}

		return nil
	})

	return hex, err
}

// eachField walks a protobuf message, handing back each field's payload.
//
// Varints and length-delimited fields are the only wire types these messages
// use; anything else is a message from a future this does not have to
// understand, and is skipped by length where it can be and refused where it
// cannot.
func eachField(b []byte, fn func(field, wire int, v []byte) error) error {
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			return fmt.Errorf("a field tag is not a varint, at %d bytes from the end", len(b))
		}

		b = b[n:]
		field, wire := int(tag>>3), int(tag&0x7) //nolint:gosec // masked to three bits

		switch wire {
		case wireVarint:
			_, n := binary.Uvarint(b)
			if n <= 0 {
				return fmt.Errorf("field %d says it is a varint and is not", field)
			}

			if err := fn(field, wire, b[:n]); err != nil {
				return err
			}

			b = b[n:]

		case wireBytes:
			size, n := binary.Uvarint(b)
			if n <= 0 {
				return fmt.Errorf("field %d has no length", field)
			}

			b = b[n:]

			if size > uint64(len(b)) {
				return fmt.Errorf(
					"field %d says it is %d bytes and %d remain"+
						"\n  the message is truncated or is not a Directory at all",
					field, size, len(b))
			}

			if err := fn(field, wire, b[:size]); err != nil {
				return err
			}

			b = b[size:]

		default:
			return fmt.Errorf(
				"field %d is wire type %d, which these messages do not use", field, wire)
		}
	}

	return nil
}

// fieldBlobDigests is FindMissingBlobsRequest.blob_digests, and
// fieldMissingBlobs is the response's list.
const (
	fieldBlobDigests  = 2
	fieldMissingBlobs = 2
)

// DigestsInRequest is the blobs a client asked about.
//
// **Only the digests.** A FindMissingBlobs request also carries an instance
// name and a digest function; this service has one store and one function, so
// neither changes the answer, and reading them would be reading fields to
// ignore them.
func DigestsInRequest(b []byte) ([]ir.NodeID, error) {
	var out []ir.NodeID

	err := eachField(b, func(field, wire int, v []byte) error {
		if field != fieldBlobDigests || wire != wireBytes {
			return nil
		}

		hex, err := hashOfDigest(v)
		if err != nil {
			return err
		}

		id, err := ir.ParseNodeID(hex)
		if err != nil {
			return fmt.Errorf("a request names %q, which is not a digest: %w", hex, err)
		}

		out = append(out, id)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// EncodeMissingBlobs writes a FindMissingBlobsResponse.
//
// **Sizes are not carried back.** A client knows what it asked about; the
// answer is which of them to send, and a size this service would have to look
// up for a blob it does not have is one it cannot state.
func EncodeMissingBlobs(missing []ir.NodeID) []byte {
	var out []byte

	for _, id := range missing {
		out = appendMessage(out, fieldMissingBlobs, encodeDigest(&scratch{}, id, 0))
	}

	return out
}

// EncodeFindMissingBlobs writes a request naming these digests.
//
// This engine is a server and not a client, so this exists for a test that has
// to ask it something - and for the day a build asks a peer the same question.
func EncodeFindMissingBlobs(ids []ir.NodeID) []byte {
	var out []byte

	for _, id := range ids {
		out = appendMessage(out, fieldBlobDigests, encodeDigest(&scratch{}, id, 0))
	}

	return out
}

// DigestsInResponse is the blobs a server said it lacks.
func DigestsInResponse(b []byte) ([]ir.NodeID, error) { return DigestsInRequest(b) }

// BatchUpdateBlobs fields.
const (
	fieldUploadRequests = 2 // BatchUpdateBlobsRequest.requests
	fieldUploadDigest   = 1 // ...Request.digest
	fieldUploadData     = 2 // ...Request.data

	fieldUploadResponses = 1 // BatchUpdateBlobsResponse.responses
	fieldResponseDigest  = 1 // ...Response.digest
	fieldResponseStatus  = 2 // ...Response.status
	fieldStatusCode      = 1 // Status.code
	fieldStatusMessage   = 2 // Status.message
)

// StatusInvalidArgument is google.rpc.Code.INVALID_ARGUMENT.
//
// Named here rather than imported: one integer does not justify the
// google/rpc dependency, and the number is part of the wire rather than of that
// library.
const StatusInvalidArgument = 3

// Upload is one blob a client asked this store to keep.
type Upload struct {
	Digest ir.NodeID
	Data   []byte
}

// UploadsInRequest is the blobs a client sent.
func UploadsInRequest(b []byte) ([]Upload, error) {
	var out []Upload

	err := eachField(b, func(field, wire int, v []byte) error {
		if field != fieldUploadRequests || wire != wireBytes {
			return nil
		}

		var u Upload

		inner := eachField(v, func(f, w int, val []byte) error {
			switch {
			case f == fieldUploadDigest && w == wireBytes:
				hex, err := hashOfDigest(val)
				if err != nil {
					return err
				}

				id, err := ir.ParseNodeID(hex)
				if err != nil {
					return fmt.Errorf("an upload names %q, which is not a digest: %w", hex, err)
				}

				u.Digest = id
			case f == fieldUploadData && w == wireBytes:
				u.Data = val
			}

			return nil
		})
		if inner != nil {
			return inner
		}

		out = append(out, u)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// Accepted is what became of one uploaded blob: a zero Code is success.
type Accepted struct {
	Digest  ir.NodeID
	Code    int
	Message string
}

// EncodeBatchUpdateBlobs writes a BatchUpdateBlobsResponse.
//
// **A result per blob, because a batch is not all-or-nothing.** One blob whose
// bytes do not name it does not make the others unusable, and a client told
// only "the batch failed" has to send every one of them again.
func EncodeBatchUpdateBlobs(results []Accepted) []byte {
	var out []byte

	for _, r := range results {
		one := appendMessage(nil, fieldResponseDigest,
			encodeDigest(&scratch{}, r.Digest, int64(len(r.Message))*0))

		if r.Code != 0 {
			st := appendVarintField(nil, fieldStatusCode, uint64(r.Code)) //nolint:gosec // a small enum
			st = appendString(st, fieldStatusMessage, r.Message)
			one = appendMessage(one, fieldResponseStatus, st)
		}

		out = appendMessage(out, fieldUploadResponses, one)
	}

	return out
}

// EncodeBatchUpdateBlobsForTest writes a request sending these blobs.
//
// This engine receives these rather than sending them; it exists so a test can
// ask the service something a client would, without a generated schema.
func EncodeBatchUpdateBlobsForTest(ups []Upload) []byte {
	var out []byte

	for _, u := range ups {
		one := appendMessage(nil, fieldUploadDigest,
			encodeDigest(&scratch{}, u.Digest, int64(len(u.Data))))
		one = appendBytes(one, fieldUploadData, u.Data)
		out = appendMessage(out, fieldUploadRequests, one)
	}

	return out
}

// BatchReadBlobs and GetActionResult fields.
const (
	fieldReadDigests   = 2 // BatchReadBlobsRequest.digests
	fieldReadResponses = 1 // BatchReadBlobsResponse.responses
	fieldReadDigest    = 1 // ...Response.digest
	fieldReadData      = 2 // ...Response.data
	fieldReadStatus    = 3 // ...Response.status

	fieldActionDigest = 2 // GetActionResultRequest.action_digest
)

// StatusNotFound is google.rpc.Code.NOT_FOUND.
const StatusNotFound = 5

// DigestsToRead is the blobs a client asked for.
func DigestsToRead(b []byte) ([]ir.NodeID, error) {
	return digestsInField(b, fieldReadDigests)
}

// ActionDigestIn is the action a client asked about.
//
// Zero and no error where the request named none: an empty request is a client
// asking about nothing, which is a miss rather than a protocol failure.
func ActionDigestIn(b []byte) (ir.NodeID, error) {
	ids, err := digestsInField(b, fieldActionDigest)
	if err != nil || len(ids) == 0 {
		return ir.NodeID{}, err
	}

	return ids[0], nil
}

// digestsInField reads every Digest carried in one field of a message.
func digestsInField(b []byte, want int) ([]ir.NodeID, error) {
	var out []ir.NodeID

	err := eachField(b, func(field, wire int, v []byte) error {
		if field != want || wire != wireBytes {
			return nil
		}

		hex, err := hashOfDigest(v)
		if err != nil {
			return err
		}

		id, err := ir.ParseNodeID(hex)
		if err != nil {
			return fmt.Errorf("a request names %q, which is not a digest: %w", hex, err)
		}

		out = append(out, id)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// Read is one blob handed back, or the reason it was not.
type Read struct {
	Digest  ir.NodeID
	Data    []byte
	Code    int
	Message string
}

// EncodeBatchReadBlobs writes a BatchReadBlobsResponse.
//
// **A result per blob, as the upload side has, and for the same reason.** A
// client asking for twenty directories and missing one wants the nineteen.
func EncodeBatchReadBlobs(reads []Read) []byte {
	var out []byte

	for _, r := range reads {
		one := appendMessage(nil, fieldReadDigest,
			encodeDigest(&scratch{}, r.Digest, int64(len(r.Data))))
		one = appendBytes(one, fieldReadData, r.Data)

		if r.Code != 0 {
			st := appendVarintField(nil, fieldStatusCode, uint64(r.Code)) //nolint:gosec // a small enum
			st = appendString(st, fieldStatusMessage, r.Message)
			one = appendMessage(one, fieldReadStatus, st)
		}

		out = appendMessage(out, fieldReadResponses, one)
	}

	return out
}

// EncodeGetActionResultForTest writes a request asking about one action.
func EncodeGetActionResultForTest(id ir.NodeID) []byte {
	return appendMessage(nil, fieldActionDigest, encodeDigest(&scratch{}, id, 0))
}

// EncodeBatchReadBlobsForTest writes a request asking for these blobs.
func EncodeBatchReadBlobsForTest(ids []ir.NodeID) []byte {
	var out []byte

	for _, id := range ids {
		out = appendMessage(out, fieldReadDigests, encodeDigest(&scratch{}, id, 0))
	}

	return out
}

// ReadsInResponse is what a BatchReadBlobs reply said about each blob.
//
// **A client must be able to tell an absent blob from an empty one**, and only
// the status says which: both carry the digest and neither carries data. A
// reader that looked at the bytes alone would take "I do not have it" for "it
// is zero bytes long", which is a perfectly valid file.
func ReadsInResponse(b []byte) ([]Read, error) {
	var out []Read

	err := eachField(b, func(field, wire int, v []byte) error {
		if field != fieldReadResponses || wire != wireBytes {
			return nil
		}

		var r Read

		inner := eachField(v, func(f, w int, val []byte) error {
			switch {
			case f == fieldReadDigest && w == wireBytes:
				hex, err := hashOfDigest(val)
				if err != nil {
					return err
				}

				id, err := ir.ParseNodeID(hex)
				if err != nil {
					return fmt.Errorf("a reply names %q, which is not a digest: %w", hex, err)
				}

				r.Digest = id
			case f == fieldReadData && w == wireBytes:
				r.Data = val
			case f == fieldReadStatus && w == wireBytes:
				return eachField(val, func(sf, sw int, sv []byte) error {
					switch {
					case sf == fieldStatusCode && sw == wireVarint:
						code, n := binary.Uvarint(sv)
						if n <= 0 {
							return fmt.Errorf("a status code is not a varint")
						}

						r.Code = int(code) //nolint:gosec // a small enum
					case sf == fieldStatusMessage && sw == wireBytes:
						r.Message = string(sv)
					}

					return nil
				})
			}

			return nil
		})
		if inner != nil {
			return inner
		}

		out = append(out, r)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}
