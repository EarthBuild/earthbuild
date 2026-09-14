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
