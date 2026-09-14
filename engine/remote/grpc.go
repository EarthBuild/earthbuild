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
