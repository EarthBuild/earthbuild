package remote

import (
	"context"
	"fmt"
	"runtime"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

	// Runner executes an action this store has no result for, and nil means
	// this service answers only from its cache.
	//
	// **An interface, because the stack an action runs over is not a question
	// about the protocol.** Which layers an action's environment is, and
	// whether the client may have one of its own, is settled by whatever starts
	// the step a client is running inside - so it is settled there, and what
	// arrives here is something that can run an action. A service that resolved
	// bases would be the second place that rule is written.
	Runner Runner

	// MaxActions bounds how many actions run at once. NumCPU when zero.
	//
	// **Their own bound, never the build's.** A client decides how many actions
	// to ask for and this one is inside a sandbox: Buck2 sizes its parallelism
	// from the machine it thinks it is on, so a step given this service can ask
	// for as many as it likes, and every one is a process on a machine already
	// running the step that asked.
	//
	// Sharing a pool with the build's steps would be worse than unbounded. A
	// step holds its slot while the actions it spawned wait for slots of their
	// own, and a build that waits for itself never finishes. Two pools can
	// oversubscribe a machine, which is slow - and prefer slow: a deadlock
	// needs a person and a stack dump, oversubscription needs patience.
	MaxActions int

	// running admits actions up to MaxActions, and is made on first use because
	// a zero Service has to work.
	once    sync.Once
	running chan struct{}
}

// Runner executes one action and reports what it produced.
//
// Named by digest and nothing else: everything an action needs is reachable
// from its Action message, and every blob of it was uploaded to this service
// before the client asked. A runner that took the message instead would be a
// runner that could be handed one the store does not hold.
type Runner interface {
	RunAction(ctx context.Context, action ir.NodeID) (layer.Result, error)
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
			Handler:    s.unary(s.getCapabilities),
		}},
	}, s)

	g.RegisterService(&grpc.ServiceDesc{
		ServiceName: "build.bazel.remote.execution.v2.ContentAddressableStorage",
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{{
			MethodName: "FindMissingBlobs",
			Handler:    s.unary(s.findMissingBlobs),
		}, {
			MethodName: "BatchUpdateBlobs",
			Handler:    s.unary(s.batchUpdateBlobs),
		}, {
			MethodName: "BatchReadBlobs",
			Handler:    s.unary(s.batchReadBlobs),
		}},
	}, s)

	g.RegisterService(&grpc.ServiceDesc{
		ServiceName: "build.bazel.remote.execution.v2.Execution",
		HandlerType: (*any)(nil),
		Streams: []grpc.StreamDesc{{
			StreamName: "Execute",
			Handler:    s.execute,
			// **Server-streaming, because an execution reports progress.** A
			// result that was ready before the call arrived is still delivered
			// this way: one Operation, already done. A client written for the
			// stream must not need a second shape for the fast case.
			ServerStreams: true,
		}},
	}, s)

	g.RegisterService(&grpc.ServiceDesc{
		ServiceName: "build.bazel.remote.execution.v2.ActionCache",
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{{
			MethodName: "GetActionResult",
			Handler:    s.unary(s.getActionResult),
		}},
	}, s)
}

// unary adapts a bytes-in, bytes-out handler to gRPC's shape.
//
// **The hold is taken here rather than in each method**, because a method that
// forgot it would be a method that lets the machine stop underneath it, and
// there is no way to notice from inside that method. One place to write it is
// one place to get it right.
func (s *Service) unary(fn func(context.Context, []byte) ([]byte, error)) grpc.MethodHandler {
	return func(
		_ any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor,
	) (any, error) {
		defer s.hold()()

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

// hold keeps the machine from stopping while this service is working.
//
// **A machine with a request in flight is not idle.** A sandbox stops itself
// when nobody has wanted it for a while, and idleness is measured by when a
// *host* last spoke - but a client inside a step is not the host. Without this
// the machine stops itself while it is busiest, and the client sees a
// connection close saying nothing.
//
// Always returns something to call, so no caller needs a nil check and none can
// omit the release by taking the wrong branch.
func (s *Service) hold() func() {
	if s.Cache == nil || s.Cache.Hold == nil {
		return func() {}
	}

	return s.Cache.Hold()
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

	// **Echoed, not reconstructed.** A client compares what comes back with
	// what it sent, and a digest is a hash *and* a size - so the reply names
	// the blobs it was asked about, exactly as it was asked about them.
	absent := map[ir.NodeID]bool{}
	for _, id := range s.Cache.Store.MissingNodes(layer.IDsOf(want)) {
		absent[id] = true
	}

	missing := make([]layer.Blob, 0, len(absent))

	for _, b := range want {
		if absent[b.ID] {
			missing = append(missing, b)
		}
	}

	return layer.EncodeMissingBlobs(missing), nil
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

// batchReadBlobs hands back the blobs a client asked for.
//
// A result per blob, as the upload side has: a client asking for twenty
// directories and missing one wants the nineteen.
func (s *Service) batchReadBlobs(_ context.Context, in []byte) ([]byte, error) {
	want, err := layer.DigestsToRead(in)
	if err != nil {
		return nil, fmt.Errorf("read a BatchReadBlobs request: %w", err)
	}

	out := make([]layer.Read, 0, len(want))

	for _, id := range want {
		r := layer.Read{Digest: id}

		// Verified on the way out as on the way in - a store that hands back
		// bytes it has not checked against the name asked for is a store of
		// whatever happens to be on the disk.
		if b, err := s.Cache.Store.Node(id); err == nil {
			r.Data = b
		} else {
			r.Code, r.Message = layer.StatusNotFound, err.Error()
		}

		out = append(out, r)
	}

	return layer.EncodeBatchReadBlobs(out), nil
}

// getActionResult is what the step under this key produced.
//
// **The key is the Action digest** (green paper 4.5a), so the number a client
// asks under is the number this engine derived: no index between them, and
// nowhere for the two to disagree.
func (s *Service) getActionResult(_ context.Context, in []byte) ([]byte, error) {
	id, err := layer.ActionDigestIn(in)
	if err != nil {
		return nil, fmt.Errorf("read a GetActionResult request: %w", err)
	}

	b, ok := s.Cache.ActionResult(id)
	if !ok {
		// **A miss is an answer, and NOT_FOUND is how this API gives it.** A
		// client reads it as "run the action", which is correct and is what
		// every build did before there was a cache.
		return nil, status.Error(codes.NotFound, "no result for this action")
	}

	return b, nil
}

// execute answers with what this action produced, running it if it must.
//
// **The cache first, and the runner only on a miss.** An engine that ran every
// action it was asked about would have a cache nothing consults; one that never
// ran anything is the cache-only service this was before there was a runner.
func (s *Service) execute(_ any, stream grpc.ServerStream) error {
	// **Running an action is the longest thing this service does**, so it is
	// the request most likely to be in flight when an idle countdown expires.
	defer s.hold()()

	var in []byte
	if err := stream.RecvMsg(&in); err != nil {
		return err
	}

	ask, err := layer.ExecutionIn(in)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "read an Execute request: %v", err)
	}

	if !ask.SkipCache {
		// **Asked for on purpose when it is skipped, usually to reproduce
		// something.** Answering from the cache anyway would answer a question
		// the client did not ask.
		if result, ok := s.Cache.ActionResult(ask.Action); ok {
			// cached_result: true, because it is. A build reporting every
			// action as executed when none of them were is a build nobody
			// trusts.
			return sendDone(stream, layer.Blob{ID: ask.Action, Size: ask.ActionSize}, result, true)
		}
	}

	if s.Runner == nil {
		return status.Error(codes.Unimplemented,
			"this service answers from its cache and holds no result for this"+
				" action, and was not given anything that can run one")
	}

	admit, err := s.admit(stream.Context())
	if err != nil {
		return err
	}

	defer admit()

	res, err := s.Runner.RunAction(stream.Context(), ask.Action)
	if err != nil {
		// **Not UNIMPLEMENTED, which means "ask somebody else".** An action
		// that could not run here because its input root is incomplete cannot
		// run anywhere, and a client told to retry elsewhere pays twice to be
		// told the same thing.
		return status.Errorf(codes.FailedPrecondition, "run this action: %v", err)
	}

	return sendDone(stream, layer.Blob{ID: ask.Action, Size: ask.ActionSize},
		layer.EncodeActionResult(res), false)
}

// sendDone answers with one Operation that is already finished.
//
// A client written for the stream must not need a second shape for the fast
// case, so a result that was ready before the call arrived is delivered exactly
// as one that took a minute.
func sendDone(stream grpc.ServerStream, action layer.Blob, result []byte, cached bool) error {
	op := layer.EncodeDoneOperation(
		"earthbuild/"+action.ID.String(), action,
		layer.EncodeExecuteResponse(result, cached))

	return stream.SendMsg(&op)
}

// admit waits for a slot to run an action in, and hands back its release.
//
// **Waits rather than refuses.** A client told RESOURCE_EXHAUSTED has to decide
// what to do about it, and what it should do is wait - so waiting here is the
// same answer with nobody having to implement it. The context is the client's,
// so a caller that gave up stops waiting with it.
func (s *Service) admit(ctx context.Context) (release func(), err error) {
	s.once.Do(func() {
		n := s.MaxActions
		if n <= 0 {
			n = runtime.NumCPU()
		}

		s.running = make(chan struct{}, n)
	})

	select {
	case s.running <- struct{}{}:
		return func() { <-s.running }, nil
	case <-ctx.Done():
		return nil, status.FromContextError(ctx.Err()).Err()
	}
}
