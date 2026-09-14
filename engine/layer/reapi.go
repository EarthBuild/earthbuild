package layer

import "encoding/binary"

// The REAPI Directory message, written by hand.
//
// **Because the bytes are the contract, and a library hides them.** A
// `Directory`'s digest is ℋ over its serialisation, so agreeing with Bazel and
// Buck2 means agreeing byte for byte with what their libraries emit - and proto3
// defines no canonical form, only a convention every implementation happens to
// follow. Writing the four messages here makes that convention something this
// repository states and tests (see TestOurDirectoryEncodingIsProtocs) rather
// than something it inherits and hopes about. It is also less code than the
// dependency: the messages are five fields wide and never change.
//
// Field numbers are from build/bazel/remote/execution/v2/remote_execution.proto
// and are transcribed in testdata/reapi/reapi_min.proto, which generates the
// vector this is checked against.
const (
	fieldFiles       = 1 // Directory.files
	fieldDirectories = 2 // Directory.directories
	fieldSymlinks    = 3 // Directory.symlinks

	fieldName          = 1 // FileNode.name, DirectoryNode.name, SymlinkNode.name
	fieldDigest        = 2 // FileNode.digest, DirectoryNode.digest
	fieldTarget        = 2 // SymlinkNode.target
	fieldIsExecutable  = 4 // FileNode.is_executable
	fieldLinkProps     = 4 // SymlinkNode.node_properties
	fieldFileProps     = 6 // FileNode.node_properties
	fieldProperties    = 1 // NodeProperties.properties
	fieldPropertyName  = 1 // NodeProperty.name
	fieldPropertyValue = 2 // NodeProperty.value
	fieldDigestHash    = 1 // Digest.hash
	fieldDigestSize    = 2 // Digest.size_bytes
)

// Wire types. Only these two occur in these messages.
const (
	wireVarint = 0
	wireBytes  = 2
)

// reapiProperty is one untyped key/value, which is where everything REAPI has
// no field for goes - uid, gid, the mode bits below is_executable, xattrs.
type reapiProperty struct{ name, value string }

// reapiFile is a FileNode: a name, the digest and size of its contents, whether
// it is executable, and whatever else we had to say about it.
type reapiFile struct {
	name       string
	hash       string // lowercase hex, as REAPI writes a digest
	size       int64
	executable bool
	props      []reapiProperty
}

// reapiDir is a DirectoryNode: a name and the digest of the Directory beneath.
// It carries no metadata at all - REAPI keeps a directory's own properties
// inside its own message, where this engine keeps them in the parent (4.5b).
type reapiDir struct {
	name string
	hash string
	size int64
}

// reapiSymlink is a SymlinkNode: a name and the target string, never what it
// points at.
type reapiSymlink struct {
	name   string
	target string
	props  []reapiProperty
}

// encodeDirectory writes a Directory message.
//
// The three lists are emitted in the order given, which REAPI requires to be by
// name - enforced by the caller, which has to sort for its own digest anyway.
func encodeDirectory(files []reapiFile, dirs []reapiDir, links []reapiSymlink) []byte {
	var out []byte

	for _, f := range files {
		out = appendMessage(out, fieldFiles, encodeFileNode(f))
	}

	for _, d := range dirs {
		out = appendMessage(out, fieldDirectories, encodeDirectoryNode(d))
	}

	for _, l := range links {
		out = appendMessage(out, fieldSymlinks, encodeSymlinkNode(l))
	}

	return out
}

func encodeFileNode(f reapiFile) []byte {
	var out []byte

	out = appendString(out, fieldName, f.name)
	out = appendMessage(out, fieldDigest, encodeDigest(f.hash, f.size))

	// **Omitted when false.** proto3 writes nothing for a field holding its
	// zero value, and a peer that emitted `is_executable: false` explicitly
	// would produce different bytes for the same file.
	if f.executable {
		out = appendVarintField(out, fieldIsExecutable, 1)
	}

	if props := encodeNodeProperties(f.props); props != nil {
		out = appendMessage(out, fieldFileProps, props)
	}

	return out
}

func encodeDirectoryNode(d reapiDir) []byte {
	out := appendString(nil, fieldName, d.name)

	return appendMessage(out, fieldDigest, encodeDigest(d.hash, d.size))
}

func encodeSymlinkNode(l reapiSymlink) []byte {
	out := appendString(nil, fieldName, l.name)
	out = appendString(out, fieldTarget, l.target)

	if props := encodeNodeProperties(l.props); props != nil {
		out = appendMessage(out, fieldLinkProps, props)
	}

	return out
}

func encodeDigest(hash string, size int64) []byte {
	out := appendString(nil, fieldDigestHash, hash)

	if size != 0 {
		out = appendVarintField(out, fieldDigestSize, uint64(size)) //nolint:gosec // never negative
	}

	return out
}

// encodeNodeProperties is nil where there is nothing to say.
//
// **Nil and empty are different bytes.** An unset message field emits nothing;
// one set to an empty message emits a tag and a zero length. A peer with no
// extra metadata - which is every tree Bazel or Buck2 constructs - must produce
// exactly the bytes we do, so "nothing to say" has to mean "write nothing".
func encodeNodeProperties(props []reapiProperty) []byte {
	if len(props) == 0 {
		return nil
	}

	var out []byte

	for _, p := range props {
		inner := appendString(nil, fieldPropertyName, p.name)
		inner = appendString(inner, fieldPropertyValue, p.value)
		out = appendMessage(out, fieldProperties, inner)
	}

	return out
}

// appendTag writes a field number and its wire type.
func appendTag(b []byte, field, wire int) []byte {
	return binary.AppendUvarint(b, uint64(field)<<3|uint64(wire)) //nolint:gosec // both are constants here
}

// appendString writes a length-delimited field, or nothing where it is empty.
func appendString(b []byte, field int, s string) []byte {
	if s == "" {
		return b // proto3 omits a field holding its zero value
	}

	b = appendTag(b, field, wireBytes)
	b = binary.AppendUvarint(b, uint64(len(s)))

	return append(b, s...)
}

// appendMessage writes a nested message, which is length-delimited like a
// string. An empty one is still written: a caller passing nil means "absent",
// and every caller here does so deliberately.
func appendMessage(b []byte, field int, msg []byte) []byte {
	b = appendTag(b, field, wireBytes)
	b = binary.AppendUvarint(b, uint64(len(msg)))

	return append(b, msg...)
}

func appendVarintField(b []byte, field int, v uint64) []byte {
	b = appendTag(b, field, wireVarint)

	return binary.AppendUvarint(b, v)
}
