package layer

import (
	"encoding/binary"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The REAPI Command, Platform and Action messages.
//
// **These carry the key**, where Directory carries the tree. Κₜ is ℋ over an
// Action (plan-remote-execution R2b), so a byte wrong here is not a malformed
// message - it is every cache entry in the store named wrongly, and the two
// halves of a fleet disagreeing about what they have already built.
//
// Exported, unlike the Directory encoding, because the key is derived in core
// and the tree is not.
const (
	fieldArguments  = 1 // Command.arguments
	fieldEnv        = 2 // Command.environment_variables
	fieldWorkingDir = 6 // Command.working_directory
	fieldOutputs    = 7 // Command.output_paths

	fieldCommandDigest = 1  // Action.command_digest
	fieldInputRoot     = 2  // Action.input_root_digest
	fieldDoNotCache    = 7  // Action.do_not_cache
	fieldSalt          = 9  // Action.salt
	fieldPlatform      = 10 // Action.platform

	fieldPlatformProps = 1 // Platform.properties
)

// Property is one name/value pair, which both Platform and Command's
// environment use.
type Property struct{ Name, Value string }

// Command is what an action runs.
//
// A step of this engine is not always a command - `file`, `image` and `local`
// operations have no argv - so a caller synthesises one for them and must not
// hand the result to a service that would try to execute it. See
// plan-remote-execution R2b.
type Command struct {
	Arguments        []string
	Env              []Property // in name order, which REAPI requires
	WorkingDirectory string
	OutputPaths      []string // empty until a step can declare what it produces
}

// Action is a Command over an input tree, and its digest is Κₜ.
type Action struct {
	Command     ir.NodeID
	CommandSize int64
	InputRoot   ir.NodeID
	InputSize   int64
	DoNotCache  bool
	// Salt is the cache generation. REAPI has this field (9) so an
	// implementation can retire one, which is exactly ζ's job - so ζ is not
	// approximated by it, it is it.
	Salt     []byte
	Platform []Property // in name order
}

// EncodeCommand writes a Command message.
func EncodeCommand(c Command) []byte {
	var out []byte

	for _, a := range c.Arguments {
		out = appendString(out, fieldArguments, a)
	}

	for _, e := range c.Env {
		out = appendMessage(out, fieldEnv, encodeProperty(e))
	}

	out = appendString(out, fieldWorkingDir, c.WorkingDirectory)

	for _, p := range c.OutputPaths {
		out = appendString(out, fieldOutputs, p)
	}

	return out
}

// EncodeAction writes an Action message.
func EncodeAction(a Action) []byte {
	var out []byte

	out = appendMessage(out, fieldCommandDigest, encodeDigest(&scratch{}, a.Command, a.CommandSize))
	out = appendMessage(out, fieldInputRoot, encodeDigest(&scratch{}, a.InputRoot, a.InputSize))

	if a.DoNotCache {
		out = appendVarintField(out, fieldDoNotCache, 1)
	}

	if len(a.Salt) > 0 {
		out = appendBytes(out, fieldSalt, a.Salt)
	}

	if len(a.Platform) > 0 {
		var props []byte

		for _, p := range a.Platform {
			props = appendMessage(props, fieldPlatformProps, encodeProperty(p))
		}

		out = appendMessage(out, fieldPlatform, props)
	}

	return out
}

// encodeProperty writes a name/value pair, which Platform.Property,
// NodeProperty and Command.EnvironmentVariable all are.
func encodeProperty(p Property) []byte {
	out := appendString(nil, fieldPropertyName, p.Name)

	return appendString(out, fieldPropertyValue, p.Value)
}

// ActionResult fields.
const (
	fieldOutputDirs = 3 // ActionResult.output_directories
	fieldExitCode   = 4 // ActionResult.exit_code
	fieldStdoutRaw  = 5 // ActionResult.stdout_raw

	fieldOutDirPath = 1 // OutputDirectory.path
	fieldOutDirRoot = 5 // OutputDirectory.root_directory_digest

	fieldExecMetadata = 6 // ActionResult.execution_metadata

	fieldMetaWorker    = 1 // ExecutedActionMetadata.worker
	fieldMetaStarted   = 3 // ExecutedActionMetadata.worker_start_timestamp
	fieldMetaCompleted = 4 // ExecutedActionMetadata.worker_completed_timestamp

	fieldStampSeconds = 1 // google.protobuf.Timestamp.seconds
	fieldStampNanos   = 2 // google.protobuf.Timestamp.nanos
)

// Result is what an action produced.
//
// **The delta is an output directory rooted at nothing.** A step of this engine
// produces a filesystem, not a declared list of files, and REAPI has a field
// for exactly that: `root_directory_digest` points at a `Directory`, which is
// what this engine's result content already is (4.5b). `tree_digest` - the
// older field - would need a `Tree` message holding every child inline, which
// says the same thing at greater length.
type Result struct {
	// Root is the Directory the step's delta materialises to.
	Root ir.NodeID
	// RootSize is that Directory's serialised length.
	RootSize int64
	// Path is where the directory sits, empty for a whole-filesystem delta.
	Path string
	// ExitCode is the step's, and is omitted when zero as proto3 requires.
	ExitCode int32
	// Stdout is what the step printed, where it was small enough to keep.
	// Empty means either it printed nothing or it printed too much - a caller
	// distinguishing those needs the entry, not the message.
	Stdout []byte
	// Worker names what ran the action, and Started and Finished are when.
	//
	// **A client may require these to be there at all.** Buck2 refuses a result
	// whose `execution_metadata` is unset - "The execution metadata are not
	// defined" - before it looks at anything in it, so a service that omitted
	// the message because it had nothing interesting to put in it is a service
	// that cannot answer. An empty message is not the same bytes as no message,
	// which is the distinction this encoding is careful about everywhere else,
	// pointing the other way for once.
	Worker            string
	Started, Finished time.Time
}

// EncodeActionResult writes an ActionResult message.
func EncodeActionResult(r Result) []byte {
	dir := appendString(nil, fieldOutDirPath, r.Path)
	dir = appendMessage(dir, fieldOutDirRoot, encodeDigest(&scratch{}, r.Root, r.RootSize))

	out := appendMessage(nil, fieldOutputDirs, dir)

	if r.ExitCode != 0 {
		out = appendVarintField(out, fieldExitCode, uint64(r.ExitCode)) //nolint:gosec // a process exit status
	}

	out = appendBytes(out, fieldStdoutRaw, r.Stdout)
	out = appendMessage(out, fieldExecMetadata, encodeExecMetadata(r))

	return out
}

// encodeExecMetadata says what ran this action and when.
//
// Always emitted, never nil: a client that requires the field requires it on a
// cache hit too, where there is no worker to name and the timestamps are of a
// run that happened on another day. Naming the engine is enough to make the
// message present, which is what is actually being asked for.
func encodeExecMetadata(r Result) []byte {
	worker := r.Worker
	if worker == "" {
		worker = "earthbuild"
	}

	out := appendString(nil, fieldMetaWorker, worker)
	out = appendStamp(out, fieldMetaStarted, r.Started)
	out = appendStamp(out, fieldMetaCompleted, r.Finished)

	return out
}

// appendStamp writes a google.protobuf.Timestamp, or nothing for a zero time.
func appendStamp(b []byte, field int, at time.Time) []byte {
	if at.IsZero() {
		return b
	}

	stamp := appendVarintField(nil, fieldStampSeconds, uint64(at.Unix()))      //nolint:gosec // after 1970
	stamp = appendVarintField(stamp, fieldStampNanos, uint64(at.Nanosecond())) //nolint:gosec // below a second

	return appendMessage(b, field, stamp)
}

// Capabilities fields.
const (
	fieldCacheCaps    = 1 // ServerCapabilities.cache_capabilities
	fieldLowAPI       = 3 // ServerCapabilities.low_api_version
	fieldHighAPI      = 4 // ServerCapabilities.high_api_version
	fieldDigestFuncs  = 1 // CacheCapabilities.digest_functions
	fieldMaxBatchSize = 4 // CacheCapabilities.max_batch_total_size_bytes
	fieldSemVerMajor  = 1 // SemVer.major
)

// DigestFunctionSHA256 and DigestFunctionBLAKE3 are the two this engine has, by
// the numbers `DigestFunction.Value` gives them.
//
// Named here rather than derived from ir.HashFunc, because these are the other
// party's numbering and ours is ours: a value that happened to match today
// would be a coincidence to maintain.
const (
	DigestFunctionSHA256 = 1
	DigestFunctionBLAKE3 = 9
)

// EncodeCapabilities writes a ServerCapabilities message.
//
// **One digest function, because a store has one.** A server advertising both
// would be offering a client a choice this engine cannot honour: every digest
// in the store was computed with the function it was built with, and answering
// under the other names nothing it holds.
func EncodeCapabilities(digestFunction int, maxBatchBytes int64) []byte {
	// **Packed, because proto3 packs a repeated scalar by default.** Written as
	// a bare varint this is field 1 wire type 0, which a conforming reader
	// takes for a different field shape entirely - and the very first message a
	// client asks for is the one it cannot read. protoc's own bytes are what
	// caught it.
	caps := appendPackedVarints(nil, fieldDigestFuncs, []uint64{uint64(digestFunction)}) //nolint:gosec // a small constant
	if maxBatchBytes != 0 {
		caps = appendVarintField(caps, fieldMaxBatchSize, uint64(maxBatchBytes)) //nolint:gosec // never negative
	}

	out := appendMessage(nil, fieldCacheCaps, caps)

	// v2 at both ends: this is the only version of the API there is.
	two := appendVarintField(nil, fieldSemVerMajor, 2)
	out = appendMessage(out, fieldLowAPI, two)

	return appendMessage(out, fieldHighAPI, two)
}

// appendPackedVarints writes a repeated scalar field the way proto3 does by
// default: one length-delimited field holding the values end to end.
func appendPackedVarints(b []byte, field int, vs []uint64) []byte {
	if len(vs) == 0 {
		return b
	}

	var packed []byte
	for _, v := range vs {
		packed = binary.AppendUvarint(packed, v)
	}

	return appendMessage(b, field, packed)
}

// Execute, ExecuteResponse and Operation fields.
const (
	fieldSkipCacheLookup = 3 // ExecuteRequest.skip_cache_lookup
	fieldExecActionDgst  = 6 // ExecuteRequest.action_digest

	fieldExecResult = 1 // ExecuteResponse.result
	fieldExecCached = 2 // ExecuteResponse.cached_result

	fieldAnyTypeURL = 1 // Any.type_url
	fieldAnyValue   = 2 // Any.value

	fieldOpName     = 1 // Operation.name
	fieldOpMetadata = 2 // Operation.metadata
	fieldOpDone     = 3 // Operation.done
	fieldOpResponse = 5 // Operation.response

	fieldExecStage      = 1 // ExecuteOperationMetadata.stage
	fieldExecMetaDigest = 2 // ExecuteOperationMetadata.action_digest

	// stageCompleted is ExecutionStage.Value.COMPLETED.
	stageCompleted = 4
)

// executeResponseType is what an `Any` holding an ExecuteResponse is called.
//
// **A constant string on the wire, and a client checks it.** An `Any` is a
// message nobody can read without being told what it is, so this is the telling.
const executeResponseType = "type.googleapis.com/build.bazel.remote.execution.v2.ExecuteResponse"

// executeMetadataType is what an `Any` holding an ExecuteOperationMetadata is
// called. A client reads `Operation.metadata` to learn how far an action has
// got, and buck2 refuses an Operation without one - "The execution metadata are
// not defined" - however complete the response beside it.
const executeMetadataType = "type.googleapis.com/build.bazel.remote.execution.v2.ExecuteOperationMetadata"

// EncodeExecuteResponse writes an ExecuteResponse carrying a result.
//
// cached says the result came from the cache rather than from running the
// action. A client reports it, and a build that shows every action as executed
// when none of them were is a build nobody trusts.
func EncodeExecuteResponse(result []byte, cached bool) []byte {
	out := appendMessage(nil, fieldExecResult, result)

	if cached {
		out = appendVarintField(out, fieldExecCached, 1)
	}

	return out
}

// EncodeDoneOperation wraps a finished ExecuteResponse as an Operation.
//
// **Execute answers with a stream of these**, so even a result that was ready
// before the call arrived is delivered as an operation that is already done.
// The name is this engine's to choose and is only useful for saying which
// action it belongs to.
func EncodeDoneOperation(name string, action Blob, response []byte) []byte {
	out := appendString(nil, fieldOpName, name)

	// **How far this action got, which a client reads before the response.**
	// An Operation with no metadata is refused by buck2 whatever is beside it,
	// and COMPLETED is the honest stage for the only kind this service sends:
	// one that is already done when it is first delivered.
	meta := appendVarintField(nil, fieldExecStage, stageCompleted)
	meta = appendMessage(meta, fieldExecMetaDigest,
		encodeDigest(&scratch{}, action.ID, action.Size))

	any := appendString(nil, fieldAnyTypeURL, executeMetadataType)
	any = appendBytes(any, fieldAnyValue, meta)
	out = appendMessage(out, fieldOpMetadata, any)

	out = appendVarintField(out, fieldOpDone, 1)

	any = appendString(any[:0], fieldAnyTypeURL, executeResponseType)
	any = appendBytes(any, fieldAnyValue, response)

	return appendMessage(out, fieldOpResponse, any)
}
