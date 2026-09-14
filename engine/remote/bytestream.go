package remote

import (
	"context"
	"errors"
	"fmt"
	"io"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// chunkBytes is how much of a blob travels in one message.
//
// Well under the batch limit this service advertises, because that figure
// bounds a whole *batch* while this bounds one message of a stream - and gRPC
// refuses a message over its own limit whatever REAPI says about batches.
const chunkBytes = 1 << 20

// readBlob streams a blob out, for the blobs a batch cannot carry.
//
// **The half of the CAS that BatchReadBlobs cannot do.** A batch is bounded at
// 4 MiB and a client splits by what it is told, so a larger blob has no way
// through it at all - which is most of what a compiler produces.
func (s *Service) readBlob(_ any, stream grpc.ServerStream) error {
	defer s.hold()()

	var in []byte
	if err := stream.RecvMsg(&in); err != nil {
		return err
	}

	ask, err := layer.ReadRequestIn(in)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "read a bytestream Read: %v", err)
	}

	id, _, err := BlobName(ask.Resource)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	b, err := s.Cache.Store.Node(id)
	if err != nil {
		// **NOT_FOUND, which a client reads as "send it".** An error here would
		// read as a service that is broken rather than a store that is empty,
		// and the two want opposite things from whoever sees them.
		return status.Errorf(codes.NotFound, "no blob %s", id)
	}

	if ask.Offset > int64(len(b)) {
		return status.Errorf(codes.OutOfRange,
			"%s is %d bytes and the read starts at %d", id, len(b), ask.Offset)
	}

	b = b[ask.Offset:]
	if ask.Limit > 0 && ask.Limit < int64(len(b)) {
		b = b[:ask.Limit]
	}

	// **At least one message, even for no bytes.** A client reading an empty
	// blob waits for something; a stream that closed without a message would
	// look like a service that gave up.
	for first := true; first || len(b) > 0; first = false {
		n := min(len(b), chunkBytes)

		msg := layer.EncodeReadResponse(b[:n])
		if err := stream.SendMsg(&msg); err != nil {
			return err
		}

		b = b[n:]
	}

	return nil
}

// writeBlob takes a blob a client streams in.
//
// Verified against the name it was sent under when the last chunk arrives,
// which is the same rule the batch path follows: accepting one is safe rather
// than trusting because the bytes are checked, and they cannot be checked until
// they are all here.
func (s *Service) writeBlob(_ any, stream grpc.ServerStream) error {
	defer s.hold()()

	var (
		resource string
		blob     []byte
	)

	for {
		var in []byte

		err := stream.RecvMsg(&in)
		if errors.Is(err, io.EOF) {
			return status.Error(codes.InvalidArgument,
				"the upload ended without finish_write, so this service cannot tell"+
					" a complete blob from an abandoned one")
		}

		if err != nil {
			return err
		}

		chunk, err := layer.WriteRequestIn(in)
		if err != nil {
			return status.Errorf(codes.InvalidArgument, "read a bytestream Write: %v", err)
		}

		// Only the first message carries the name; demanding it on every one
		// would reject every upload after its first chunk.
		if chunk.Resource != "" {
			resource = chunk.Resource
		}

		// **The offset is checked, not trusted.** A client that resumed from
		// somewhere this service never got to would otherwise have its blob
		// silently assembled with a hole in it, and the digest check at the end
		// would report corruption rather than the gap that caused it.
		if chunk.Offset != int64(len(blob)) {
			return status.Errorf(codes.InvalidArgument,
				"this upload is at %d bytes and the next chunk says it starts at %d",
				len(blob), chunk.Offset)
		}

		blob = append(blob, chunk.Data...)

		if chunk.Finish {
			break
		}
	}

	id, size, err := BlobName(resource)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	if size != int64(len(blob)) {
		return status.Errorf(codes.InvalidArgument,
			"%s was sent as %d bytes and %d arrived", id, size, len(blob))
	}

	if err := s.Cache.Store.Accept(id, blob); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	out := layer.EncodeWriteResponse(int64(len(blob)))

	return stream.SendMsg(&out)
}

// queryWriteStatus says how much of an upload this service has.
//
// **Nothing, always, and that is a true answer.** A resumable upload needs the
// server to keep a partial blob under the client's uuid between calls; this one
// holds a stream's bytes only while the stream is open, so an upload that was
// interrupted was not kept. Saying so sends the client back to the beginning,
// which is what actually happened - and a blob already in the store is reported
// complete, so the common reason for asking is answered properly.
func (s *Service) queryWriteStatus(_ context.Context, in []byte) ([]byte, error) {
	ask, err := layer.ReadRequestIn(in) // resource_name is field 1 in both
	if err != nil {
		return nil, fmt.Errorf("read a QueryWriteStatus: %w", err)
	}

	id, _, err := BlobName(ask.Resource)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if b, err := s.Cache.Store.Node(id); err == nil {
		return layer.EncodeQueryWriteStatus(int64(len(b)), true), nil
	}

	return layer.EncodeQueryWriteStatus(0, false), nil
}
