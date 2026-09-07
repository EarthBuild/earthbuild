package fleet

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// ErrMalformed marks bytes that are not an assignment.
//
// **Never a panic.** These bytes come from a peer, so every way they can be
// wrong is a case rather than an accident: truncated, over-long, claiming a
// count nobody could satisfy. A decoder that panicked on a bad length would let
// any peer stop the driver by sending four bytes.
var ErrMalformed = errors.New("this is not a well-formed assignment")

// maxCount bounds any length read from the wire.
//
// A count is four bytes and a peer chooses them, so `make([]T, n)` on an
// unchecked one is an allocation of up to four gigabytes decided by somebody
// else. The bound is far above anything real - a step with a million arguments
// is not a step - and far below anything that hurts.
const maxCount = 1 << 20

// decoder reads the canonical encoding, refusing anything it cannot.
type decoder struct {
	b   []byte
	err error
}

// Decode reads an assignment written by Encode.
//
// Mirrors Encode field for field and in the same order, which is the only thing
// keeping the two in step - there is no schema, and there is a round-trip test
// that fails the moment they disagree.
func Decode(b []byte) (Assignment, error) {
	d := &decoder{b: b}

	var a Assignment

	// Fixed-width, and deliberately not bounded like a count: see Encode.
	a.Version = int(d.int64())

	a.Base = d.ids()
	a.Sources = make([][]ir.NodeID, 0, min(d.count(), 64))

	for range cap(a.Sources) {
		a.Sources = append(a.Sources, d.ids())
	}

	a.Op = d.op()
	a.Platform = d.str()
	a.DeadlineUnix = d.int64()

	a.Hints.Images = d.strs()
	a.Hints.ReadsPredicted = d.strs()
	a.Hints.EstimatedSeconds = d.int64()
	a.Hints.Holders = d.strs()

	if d.err != nil {
		return Assignment{}, d.err
	}

	// Trailing bytes are as wrong as missing ones: an assignment is exactly its
	// encoding, and a peer that appended something is not speaking this
	// protocol.
	if len(d.b) != 0 {
		return Assignment{}, fmt.Errorf("%w: %d bytes after the end", ErrMalformed, len(d.b))
	}

	return a, nil
}

func (d *decoder) take(n int) []byte {
	if d.err != nil {
		return nil
	}

	if n < 0 || n > len(d.b) {
		d.err = fmt.Errorf("%w: wanted %d bytes and %d remain", ErrMalformed, n, len(d.b))

		return nil
	}

	out := d.b[:n]
	d.b = d.b[n:]

	return out
}

func (d *decoder) count() int {
	b := d.take(4)
	if b == nil {
		return 0
	}

	n := int(binary.BigEndian.Uint32(b))
	if n > maxCount {
		d.err = fmt.Errorf("%w: a count of %d, and %d is the most this engine"+
			" will allocate for a peer", ErrMalformed, n, maxCount)

		return 0
	}

	return n
}

func (d *decoder) int64() int64 {
	b := d.take(8)
	if b == nil {
		return 0
	}

	return int64(binary.BigEndian.Uint64(b)) //nolint:gosec // two's complement, as written
}

func (d *decoder) str() string {
	n := d.count()

	b := d.take(n)
	if b == nil {
		return ""
	}

	return string(b)
}

func (d *decoder) strs() []string {
	n := d.count()

	out := make([]string, 0, min(n, 64))
	for range n {
		out = append(out, d.str())
	}

	return out
}

func (d *decoder) ids() []ir.NodeID {
	n := d.count()

	out := make([]ir.NodeID, 0, min(n, 64))

	for range n {
		b := d.take(len(ir.NodeID{}))
		if b == nil {
			return out
		}

		var id ir.NodeID

		copy(id[:], b)
		out = append(out, id)
	}

	return out
}

func (d *decoder) op() Op {
	var op Op

	op.Kind = Kind(d.str())

	op.Args = d.strs()

	n := d.count()
	if n > 0 {
		op.Env = make(map[string]string, min(n, 64))
	}

	for range n {
		k := d.str()
		op.Env[k] = d.str()
	}

	op.Dir = d.str()
	op.User = d.str()
	op.NoNetwork = d.boolean()
	op.Scratch = d.strs()

	return op
}

func (d *decoder) boolean() bool {
	b := d.take(1)

	return b != nil && b[0] != 0
}
