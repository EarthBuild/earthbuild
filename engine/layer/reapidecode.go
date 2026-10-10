package layer

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"

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
// Blob is a digest as REAPI defines one: a hash *and* a size.
//
// **Both, because a peer compares the whole message.** A reply that echoes the
// hash and drops the size is a reply about a blob the client never asked about
// - proto3 omits a zero, so only the empty blob ever matched. Carrying the size
// separately from the hash is what made that possible to write.
type Blob struct {
	ID   ir.NodeID
	Size int64
}

// IDsOf is the hashes of these blobs, for a store that files things by hash.
func IDsOf(blobs []Blob) []ir.NodeID {
	out := make([]ir.NodeID, len(blobs))
	for i, b := range blobs {
		out[i] = b.ID
	}

	return out
}

func DigestsInRequest(b []byte) ([]Blob, error) {
	var out []Blob

	err := eachField(b, func(field, wire int, v []byte) error {
		if field != fieldBlobDigests || wire != wireBytes {
			return nil
		}

		id, size, err := digestIn(v)
		if err != nil {
			return fmt.Errorf("a request names something that is not a digest: %w", err)
		}

		out = append(out, Blob{ID: id, Size: size})

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
func EncodeMissingBlobs(missing []Blob) []byte {
	var out []byte

	for _, b := range missing {
		out = appendMessage(out, fieldMissingBlobs, encodeDigest(&scratch{}, b.ID, b.Size))
	}

	return out
}

// EncodeFindMissingBlobs writes a request naming these digests.
//
// This engine is a server and not a client, so this exists for a test that has
// to ask it something - and for the day a build asks a peer the same question.
func EncodeFindMissingBlobs(blobs []Blob) []byte {
	var out []byte

	for _, b := range blobs {
		out = appendMessage(out, fieldBlobDigests, encodeDigest(&scratch{}, b.ID, b.Size))
	}

	return out
}

// DigestsInResponse is the blobs a server said it lacks.
func DigestsInResponse(b []byte) ([]Blob, error) { return DigestsInRequest(b) }

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

// Execution is what a client asked this service to run.
type Execution struct {
	Action ir.NodeID
	// ActionSize is the Action blob's length, which travels with its hash: an
	// Operation quotes the digest back, and a client compares the whole thing.
	ActionSize int64
	// SkipCache says the client wants the action run even where a result is
	// already known. A service that ignored it would answer a question the
	// client did not ask - usually because it is trying to reproduce something.
	SkipCache bool
}

// ExecutionIn reads an ExecuteRequest.
func ExecutionIn(b []byte) (Execution, error) {
	var out Execution

	err := eachField(b, func(field, wire int, v []byte) error {
		switch {
		case field == fieldExecActionDgst && wire == wireBytes:
			id, size, err := digestIn(v)
			if err != nil {
				return fmt.Errorf("an Execute names something that is not a digest: %w", err)
			}

			out.Action, out.ActionSize = id, size
		case field == fieldSkipCacheLookup && wire == wireVarint:
			n, read := binary.Uvarint(v)
			if read <= 0 {
				return fmt.Errorf("skip_cache_lookup is not a varint")
			}

			out.SkipCache = n != 0
		}

		return nil
	})
	if err != nil {
		return Execution{}, err
	}

	return out, nil
}

// EncodeExecuteForTest writes a request to run one action.
func EncodeExecuteForTest(id ir.NodeID, skipCache bool) []byte {
	out := appendMessage(nil, fieldExecActionDgst, encodeDigest(&scratch{}, id, 0))
	if skipCache {
		out = appendVarintField(out, fieldSkipCacheLookup, 1)
	}

	return out
}

// Finished is what an Operation said about a completed action.
type Finished struct {
	Name   string
	Done   bool
	Cached bool
	// Result is the ActionResult bytes, for a caller that wants the outputs.
	Result []byte
}

// FinishedIn reads an Operation carrying an ExecuteResponse.
//
// **A client has to know whether the action ran.** `cached_result` is how the
// API says it, and a build reporting every action as executed when none of them
// were is a build nobody trusts.
func FinishedIn(b []byte) (Finished, error) {
	var out Finished

	err := eachField(b, func(field, wire int, v []byte) error {
		switch {
		case field == fieldOpName && wire == wireBytes:
			out.Name = string(v)
		case field == fieldOpDone && wire == wireVarint:
			n, read := binary.Uvarint(v)
			if read <= 0 {
				return fmt.Errorf("done is not a varint")
			}

			out.Done = n != 0
		case field == fieldOpResponse && wire == wireBytes:
			return eachField(v, func(af, aw int, av []byte) error {
				if af != fieldAnyValue || aw != wireBytes {
					return nil
				}

				return eachField(av, func(rf, rw int, rv []byte) error {
					switch {
					case rf == fieldExecResult && rw == wireBytes:
						out.Result = rv
					case rf == fieldExecCached && rw == wireVarint:
						n, read := binary.Uvarint(rv)
						if read <= 0 {
							return fmt.Errorf("cached_result is not a varint")
						}

						out.Cached = n != 0
					}

					return nil
				})
			})
		}

		return nil
	})
	if err != nil {
		return Finished{}, err
	}

	return out, nil
}

// Member is one entry of a Directory, as a client sent it.
type Member struct {
	Name string
	// Digest is a file's contents or a subdirectory's Directory message.
	Digest ir.NodeID
	// Target is a symlink's, and is empty for anything else.
	Target string
	// Executable is REAPI's single mode bit; Mode is this engine's own, where
	// the sender was this engine and said so.
	Executable bool
	Mode       uint32
}

// Directory is a Directory message read back.
type Directory struct {
	Files []Member
	Dirs  []Member
	Links []Member
}

// DirectoryIn reads a Directory message.
//
// **Enough to write the tree out again**, which is what materialising an input
// root is. ChildDigests reads only the subdirectories, because walking a tree
// needs nothing else; this reads what a file is called, what it holds and
// whether it may be run.
func DirectoryIn(b []byte) (Directory, error) {
	var out Directory

	// **Names are checked here, so that holding a Directory is the guarantee.**
	// Anywhere else and every consumer has to repeat the check, and the one
	// that forgets is the one that writes to the disk.
	seen := map[string]bool{}

	err := eachField(b, func(field, wire int, v []byte) error {
		if wire != wireBytes {
			return nil
		}

		if field != fieldFiles && field != fieldDirectories && field != fieldSymlinks {
			return nil
		}

		digestField := fieldDigest
		if field == fieldSymlinks {
			digestField = 0
		}

		m, err := memberIn(v, digestField)
		if err != nil {
			return err
		}

		if err := checkName(m.Name, seen); err != nil {
			return err
		}

		switch field {
		case fieldFiles:
			out.Files = append(out.Files, m)
		case fieldDirectories:
			out.Dirs = append(out.Dirs, m)
		case fieldSymlinks:
			out.Links = append(out.Links, m)
		}

		return nil
	})
	if err != nil {
		return Directory{}, err
	}

	return out, nil
}

// checkName refuses a member name that is not one path segment, or one already
// used in this directory.
//
// **REAPI defines a name as a single component and leaves the check to the
// server, which is here.** The bytes of a Directory hash to the name it was
// filed under whatever the names inside it say, so verification passes for a
// message describing a tree that reaches outside itself: a member called
// `../../etc/whatever` lands two directories up in anything that joins the name
// to a path. Nothing downstream can tell, because there is nothing to see - the
// message is well formed and says what it says.
//
// Uniqueness belongs with it because it is load-bearing rather than tidy.
// Subdirectories are materialised into paths just created, so a member cannot
// be reached through a symlink a sibling planted - unless two members share a
// name, and the second write follows what the first one left.
func checkName(name string, seen map[string]bool) error {
	const sep = `/\` + "\x00"

	switch {
	case name == "":
		return errors.New("a member of this directory has no name")
	case name == "." || name == "..":
		return fmt.Errorf(
			"%q is a member name, and a name is one path segment\n"+
				"  `.` and `..` name this directory and its parent, not anything in it",
			name)
	case strings.ContainsAny(name, sep):
		return fmt.Errorf(
			"%q is a member name, and a name is one path segment\n"+
				"  a separator in one describes a tree reaching outside the one being sent",
			name)
	case seen[name]:
		return fmt.Errorf(
			"%q names two members of this directory\n"+
				"  the second would be written over, or through, the first",
			name)
	}

	seen[name] = true

	return nil
}

// memberIn reads one FileNode, DirectoryNode or SymlinkNode.
//
// digestField is 0 for a symlink, which carries a target where the others carry
// a digest - the two are the same field number and different meanings, which is
// why the caller says which it is expecting rather than this guessing.
func memberIn(b []byte, digestField int) (Member, error) {
	var m Member

	err := eachField(b, func(f, w int, v []byte) error {
		switch {
		case f == fieldName && w == wireBytes:
			m.Name = string(v)
		case digestField != 0 && f == digestField && w == wireBytes:
			hex, err := hashOfDigest(v)
			if err != nil {
				return err
			}

			id, err := ir.ParseNodeID(hex)
			if err != nil {
				return fmt.Errorf("%q names %q, which is not a digest: %w", m.Name, hex, err)
			}

			m.Digest = id
		case digestField == 0 && f == fieldTarget && w == wireBytes:
			m.Target = string(v)
		case f == fieldIsExecutable && w == wireVarint:
			n, read := binary.Uvarint(v)
			if read <= 0 {
				return fmt.Errorf("is_executable is not a varint")
			}

			m.Executable = n != 0
		case f == fieldFileProps && w == wireBytes:
			return eachField(v, func(pf, pw int, pv []byte) error {
				if pf != fieldProperties || pw != wireBytes {
					return nil
				}

				name, value, err := propertyIn(pv)
				if err != nil {
					return err
				}

				if name == propPrefix+"mode" {
					mode, convErr := strconv.ParseUint(value, 8, 32)
					if convErr != nil {
						return fmt.Errorf("%q has mode %q, which is not a mode: %w",
							m.Name, value, convErr)
					}

					m.Mode = uint32(mode)
				}

				return nil
			})
		}

		return nil
	})
	if err != nil {
		return Member{}, err
	}

	return m, nil
}

// propertyIn reads one NodeProperty.
func propertyIn(b []byte) (name, value string, err error) {
	err = eachField(b, func(f, w int, v []byte) error {
		switch {
		case f == fieldPropertyName && w == wireBytes:
			name = string(v)
		case f == fieldPropertyValue && w == wireBytes:
			value = string(v)
		}

		return nil
	})

	return name, value, err
}
