package fleet

import (
	"bytes"
	"slices"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Encode writes an assignment in the canonical serialisation of B.1.
//
// **The same 𝒮 that derives keys.** `ir.Encoder` is the injective encoding of
// §1.4 with a hash on the end of it; this is the same bytes into a buffer. They
// are one function in the specification and are one function here, because a
// rule implemented twice drifts - and the two failure modes are a
// per-implementation cache and a peer that disagrees about what a step is.
//
// Every field is written unconditionally, including the empty ones. A field
// that appears only when set makes the field after it shift position, so an
// absent `Dir` followed by a user named `x` would encode as a `Dir` of `x` -
// which is the same argument `Bool` makes one level down and the reason nothing
// here is `omitempty`.
func Encode(a Assignment) []byte {
	var buf bytes.Buffer

	e := ir.NewEncoder(&buf)

	// A fixed-width integer, not a count. `Count` means "this many follow" and
	// is bounded on the reading side by what a peer may make this engine
	// allocate - which is the right rule for a length and nonsense for a
	// version, where it would refuse a vocabulary numbered above the allocation
	// bound. `testing/quick` found this; every hand-written case used version 1
	// (E245).
	e.Fixed(bigEndian64(int64(a.Version)))

	e.Count(len(a.Base))

	for _, id := range a.Base {
		e.Fixed(id[:])
	}

	e.Count(len(a.Sources))

	for _, src := range a.Sources {
		e.Count(len(src))

		for _, id := range src {
			e.Fixed(id[:])
		}
	}

	encodeOp(e, a.Op)

	e.Str(a.Platform)
	e.Fixed(bigEndian64(a.DeadlineUnix))

	// Hints are advisory and may be dropped by any participant without changing
	// the result (I5) - but they are *in* the assignment, so two assignments
	// differing only in their hints are different messages. Encoding them keeps
	// the encoding injective over the type; it does not make them load-bearing.
	e.Count(len(a.Hints.Images))

	for _, img := range a.Hints.Images {
		e.Str(img)
	}

	e.Count(len(a.Hints.ReadsPredicted))

	for _, p := range a.Hints.ReadsPredicted {
		e.Str(p)
	}

	e.Fixed(bigEndian64(a.Hints.EstimatedSeconds))

	e.Count(len(a.Hints.Holders))

	for _, h := range a.Hints.Holders {
		e.Str(h)
	}

	return buf.Bytes()
}

func encodeOp(e *ir.Encoder, op Op) {
	e.Str(string(op.Kind))

	e.Count(len(op.Args))

	for _, a := range op.Args {
		e.Str(a)
	}

	// Ascending key order, which is B.1's requirement and not a convenience: Go
	// randomises map iteration, so an encoding that walked the map directly
	// would differ between two runs of the same process.
	keys := make([]string, 0, len(op.Env))
	for k := range op.Env {
		keys = append(keys, k)
	}

	slices.Sort(keys)
	e.Count(len(keys))

	for _, k := range keys {
		e.Str(k)
		e.Str(op.Env[k])
	}

	e.Str(op.Dir)
	e.Str(op.User)
	e.Bool(op.NoNetwork)

	// In the order given. Two mounts at the same paths in a different order are
	// the same step, but this encoding is also an identity, and sorting here
	// would be a claim about the caller's data made in the wrong place.
	e.Count(len(op.Scratch))

	for _, t := range op.Scratch {
		e.Str(t)
	}
}

// bigEndian64 is a signed integer as B.1 wants it: fixed width, big-endian.
func bigEndian64(v int64) []byte {
	var b [8]byte

	u := uint64(v) //nolint:gosec // two's complement, which is the wire's form
	for i := range b {
		b[7-i] = byte(u >> (8 * i)) //nolint:gosec // one byte at a time
	}

	return b[:]
}
