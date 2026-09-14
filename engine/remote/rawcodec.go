package remote

import (
	"fmt"

	"google.golang.org/grpc/encoding"
)

// raw is a gRPC codec that carries messages as bytes.
//
// **So that the encodings stay one definition.** This engine already encodes
// and decodes the REAPI messages it uses, checked against protoc's own output;
// generating a second set from the schema would put two definitions of the wire
// format in one binary, and they agree until somebody regenerates one. The
// transport does not need to understand a message to move it.
//
// It also keeps the dependency at grpc alone: the real schema imports
// google/api, google/longrunning and google/rpc, none of which a request for a
// blob needs.
type raw struct{}

// Name is what a peer negotiates. **Deliberately "proto"**: a client sends
// protobuf and expects protobuf, and these *are* protobuf bytes - the codec is
// a no-op over them, not a different format. Announcing anything else would
// have every REAPI client refuse a service that speaks their language perfectly.
func (raw) Name() string { return "proto" }

func (raw) Marshal(v any) ([]byte, error) {
	b, ok := v.(*[]byte)
	if !ok {
		return nil, fmt.Errorf("this service sends bytes, and was given %T", v)
	}

	return *b, nil
}

func (raw) Unmarshal(data []byte, v any) error {
	b, ok := v.(*[]byte)
	if !ok {
		return fmt.Errorf("this service receives bytes, and was given %T", v)
	}

	// Copied because gRPC reuses its read buffer, and a handler that kept the
	// slice would find it rewritten underneath by the next message.
	*b = append([]byte(nil), data...)

	return nil
}

// Codec is the raw codec, for a client that has to agree with this service.
func Codec() encoding.Codec { return raw{} }
