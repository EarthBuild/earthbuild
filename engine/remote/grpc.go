package remote

import (
	"context"
	"fmt"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"google.golang.org/grpc"
)

// maxBatch is how much this service will move in one message.
//
// REAPI's own conventional figure. A client reads it from Capabilities and
// splits its requests accordingly, so the number is a promise rather than a
// preference.
const maxBatch = 4 << 20

// Service is this store, spoken to over gRPC.
//
// **The transport, and nothing else.** The messages are encoded and decoded by
// engine/layer, checked against protoc's own output; this moves them. gRPC
// carries them as bytes (see raw), so there is no generated schema in the
// binary to disagree with the one that is tested.
type Service struct {
	Cache *Cache
}

// Register adds this service's methods to a gRPC server.
//
// Hand-built descriptors rather than generated ones, for the reason the codec
// is raw: a second definition of these messages is a second thing to keep true.
func (s *Service) Register(g grpc.ServiceRegistrar) {
	g.RegisterService(&grpc.ServiceDesc{
		ServiceName: "build.bazel.remote.execution.v2.Capabilities",
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{{
			MethodName: "GetCapabilities",
			Handler:    unary(s.getCapabilities),
		}},
	}, s)

	g.RegisterService(&grpc.ServiceDesc{
		ServiceName: "build.bazel.remote.execution.v2.ContentAddressableStorage",
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{{
			MethodName: "FindMissingBlobs",
			Handler:    unary(s.findMissingBlobs),
		}, {
			MethodName: "BatchUpdateBlobs",
			Handler:    unary(s.batchUpdateBlobs),
		}},
	}, s)
}

// unary adapts a bytes-in, bytes-out handler to gRPC's shape.
func unary(fn func(context.Context, []byte) ([]byte, error)) grpc.MethodHandler {
	return func(
		_ any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor,
	) (any, error) {
		var in []byte
		if err := dec(&in); err != nil {
			return nil, err
		}

		out, err := fn(ctx, in)
		if err != nil {
			return nil, err
		}

		return &out, nil
	}
}

// getCapabilities says which digest function this store was built with.
//
// **One, not a menu.** Every digest in the store was computed with the function
// it was built with, so offering a choice would be offering a client one this
// service cannot honour - and a client that picked the other would find a store
// holding nothing.
func (s *Service) getCapabilities(context.Context, []byte) ([]byte, error) {
	fn := layer.DigestFunctionBLAKE3
	if ir.Hash() == ir.HashSHA256 {
		fn = layer.DigestFunctionSHA256
	}

	return layer.EncodeCapabilities(fn, maxBatch), nil
}

// findMissingBlobs is which of these a client still has to send.
//
// The question a sender asks before a transfer, and the reason a tree is worth
// naming by its parts: a peer holding all but one directory is told about the
// one.
func (s *Service) findMissingBlobs(_ context.Context, in []byte) ([]byte, error) {
	want, err := layer.DigestsInRequest(in)
	if err != nil {
		return nil, fmt.Errorf("read a FindMissingBlobs request: %w", err)
	}

	return layer.EncodeMissingBlobs(s.Cache.Store.MissingNodes(want)), nil
}

// batchUpdateBlobs keeps the blobs a client sent.
//
// **Writes are accepted here and refused over HTTP, and the difference is not
// inconsistency.** The HTTP cache is a store filled by builds, where an upload
// would be a stranger's claim about what a name means. This service exists for
// a client inside a step that this engine started - it has to send its input
// root before anything can run over it, and the sandbox boundary is the only
// boundary there is.
//
// Every blob is verified against the name it was sent under, which is what
// makes accepting one safe rather than trusting.
//
// A result per blob, because a batch is not all-or-nothing: one blob whose
// bytes do not name it does not make the others unusable, and a client told
// only that the batch failed has to send every one of them again.
func (s *Service) batchUpdateBlobs(_ context.Context, in []byte) ([]byte, error) {
	ups, err := layer.UploadsInRequest(in)
	if err != nil {
		return nil, fmt.Errorf("read a BatchUpdateBlobs request: %w", err)
	}

	out := make([]layer.Accepted, 0, len(ups))

	for _, u := range ups {
		r := layer.Accepted{Digest: u.Digest}

		if err := s.Cache.Store.Accept(u.Digest, u.Data); err != nil {
			r.Code, r.Message = layer.StatusInvalidArgument, err.Error()
		}

		out = append(out, r)
	}

	return layer.EncodeBatchUpdateBlobs(out), nil
}
