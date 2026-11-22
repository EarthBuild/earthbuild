package layer

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// google.bytestream, which is how a blob past the batch limit travels.
//
// **The half of the CAS that BatchUpdateBlobs cannot do.** A batch is bounded -
// this service says 4 MiB and a client splits by what it is told - so anything
// larger has no way through it at all. A compiler's output is routinely larger,
// which is why a service without this works on an example and not on a build.
//
// Field numbers checked against googleapis/google/bytestream/bytestream.proto
// rather than recalled: `data` is 10 in both the request and the response,
// which is the one that is never where it looks like it should be.
const (
	fieldStreamResource = 1  // Read/Write/QueryWriteStatus resource_name
	fieldReadOffset     = 2  // ReadRequest.read_offset
	fieldReadLimit      = 3  // ReadRequest.read_limit
	fieldWriteOffset    = 2  // WriteRequest.write_offset
	fieldFinishWrite    = 3  // WriteRequest.finish_write
	fieldStreamData     = 10 // ReadResponse.data, WriteRequest.data

	fieldCommittedSize = 1 // WriteResponse.committed_size
	fieldWriteComplete = 2 // QueryWriteStatusResponse.complete
)

// ReadAsk is what a client wants out of the stream.
type ReadAsk struct {
	Resource string
	Offset   int64
	Limit    int64
}

// ReadRequestIn reads a bytestream ReadRequest.
func ReadRequestIn(b []byte) (ReadAsk, error) {
	var out ReadAsk

	err := eachField(b, func(field, wire int, v []byte) error {
		switch {
		case field == fieldStreamResource && wire == wireBytes:
			out.Resource = string(v)
		case field == fieldReadOffset && wire == wireVarint:
			n, err := varint(v, "read_offset")
			if err != nil {
				return err
			}

			out.Offset = n
		case field == fieldReadLimit && wire == wireVarint:
			n, err := varint(v, "read_limit")
			if err != nil {
				return err
			}

			out.Limit = n
		}

		return nil
	})
	if err != nil {
		return ReadAsk{}, err
	}

	return out, nil
}

// EncodeReadResponse writes one chunk of a blob.
func EncodeReadResponse(data []byte) []byte {
	return appendBytes(nil, fieldStreamData, data)
}

// WriteChunk is one message of an upload.
type WriteChunk struct {
	Resource string
	Offset   int64
	Finish   bool
	Data     []byte
}

// WriteRequestIn reads a bytestream WriteRequest.
//
// Only the first message of a stream carries the resource name; the rest are
// offset, data and eventually finish_write, and a server that demanded the name
// on every one would reject every upload after the first chunk.
func WriteRequestIn(b []byte) (WriteChunk, error) {
	var out WriteChunk

	err := eachField(b, func(field, wire int, v []byte) error {
		switch {
		case field == fieldStreamResource && wire == wireBytes:
			out.Resource = string(v)
		case field == fieldWriteOffset && wire == wireVarint:
			n, err := varint(v, "write_offset")
			if err != nil {
				return err
			}

			out.Offset = n
		case field == fieldFinishWrite && wire == wireVarint:
			n, err := varint(v, "finish_write")
			if err != nil {
				return err
			}

			out.Finish = n != 0
		case field == fieldStreamData && wire == wireBytes:
			// Copied: `v` points into the caller's buffer, and a chunk is kept
			// until the whole blob has arrived.
			out.Data = append([]byte(nil), v...)
		}

		return nil
	})
	if err != nil {
		return WriteChunk{}, err
	}

	return out, nil
}

// EncodeWriteResponse says how much of the blob this service has.
func EncodeWriteResponse(committed int64) []byte {
	return appendVarintField(nil, fieldCommittedSize, uint64(committed)) //nolint:gosec // a length
}

// EncodeQueryWriteStatus answers how far an upload got.
func EncodeQueryWriteStatus(committed int64, complete bool) []byte {
	out := appendVarintField(nil, fieldCommittedSize, uint64(committed)) //nolint:gosec // a length

	if complete {
		out = appendVarintField(out, fieldWriteComplete, 1)
	}

	return out
}

// varint reads a field this engine expects to be one.
func varint(v []byte, what string) (int64, error) {
	n, read := binary.Uvarint(v)
	if read <= 0 {
		return 0, fmt.Errorf("%s is not a varint", what)
	}

	if int64(n) < 0 { //nolint:gosec // the check is the point
		return 0, errors.New(what + " is larger than any blob")
	}

	return int64(n), nil //nolint:gosec // checked above
}

// ReadResponseIn reads one chunk out of a Read reply.
//
// The reply half, for a test that drives this engine through its own protocol -
// which is the only way to check that what a peer would read is what was meant.
func ReadResponseIn(b []byte) ([]byte, error) {
	var out []byte

	err := eachField(b, func(field, wire int, v []byte) error {
		if field == fieldStreamData && wire == wireBytes {
			out = append([]byte(nil), v...)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// EncodeReadRequestForTest writes a bytestream ReadRequest, for tests that ask
// this service for a blob the way a client does.
func EncodeReadRequestForTest(resource string, offset, limit int64) []byte {
	out := appendString(nil, fieldStreamResource, resource)

	if offset != 0 {
		out = appendVarintField(out, fieldReadOffset, uint64(offset)) //nolint:gosec // a length
	}

	if limit != 0 {
		out = appendVarintField(out, fieldReadLimit, uint64(limit)) //nolint:gosec // a length
	}

	return out
}

// EncodeWriteRequestForTest writes one message of an upload.
func EncodeWriteRequestForTest(resource string, offset int64, data []byte, finish bool) []byte {
	out := appendString(nil, fieldStreamResource, resource)

	if offset != 0 {
		out = appendVarintField(out, fieldWriteOffset, uint64(offset)) //nolint:gosec // a length
	}

	if finish {
		out = appendVarintField(out, fieldFinishWrite, 1)
	}

	return appendBytes(out, fieldStreamData, data)
}
