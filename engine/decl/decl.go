// Package decl is γ, a declaration: what an image or an Earthfile says about how
// a step should run, as opposed to what it puts in the filesystem.
//
// Green paper §3.2a. A declaration is a stack element like a layer, so it
// travels by the mechanism that moves stack elements and reaches every key
// derived from the stack through ids(𝑏). One mechanism serves both what an image
// declares and what a build declares, because they say the same kind of thing
// about the same step.
package decl

import (
	"bytes"
	"fmt"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Declaration is γ.
//
// **Env is as written, before expansion** (3.10). `ENV MYPATH=hello:$PATH`
// expanded where it is written down names its own base, so the same line on two
// bases would be two declarations; expanded in the fold it is one that means the
// right thing on both - and the fold is the only place the value of `$PATH` is
// known, since it is whatever the elements before it left.
//
// **A secret never appears here** (I19). A declaration is stored,
// content-addressed and shared, so a secret value in one is published to every
// machine that materialises the stack. Secrets reach a step by their own
// mechanism and enter ε by identity alone.
type Declaration struct {
	// Env is a sequence of `NAME=value`, in the order written. Not sorted: later
	// wins, so `A=1` then `A=2` says something different from the reverse, and a
	// canonical form that sorted them would make the two one.
	Env []string
	// WorkingDir is where the step starts, when the declaration names one.
	WorkingDir string
	// User is who it runs as.
	User string
	// Entrypoint and Cmd are what an image says it is for. Carried because a
	// declaration is what the image declares, not a selection from it: an engine
	// that kept only what it currently reads would have to be revisited by
	// whoever needs the rest, and their absence would look like an image that
	// said nothing.
	Entrypoint []string
	Cmd        []string
}

// magic separates this encoding from every other thing that gets hashed, and
// names its version: a declaration's identity is a stored name, so a change to
// what 𝒮(γ) covers must produce different identities rather than quietly
// reinterpreting the old ones.
const magic = "EBDECL1"

// Encode is 𝒮(γ): the canonical serialisation.
//
// Every element is length-prefixed and every sequence is counted, so no two
// distinct declarations encode alike (§1.4). Fields in a fixed order, because
// the order is part of the encoding and not of the struct.
func Encode(d Declaration) []byte {
	var buf bytes.Buffer

	e := ir.NewEncoder(&buf)

	e.Fixed([]byte(magic))
	writeSeq(e, d.Env)
	e.Str(d.WorkingDir)
	e.Str(d.User)
	writeSeq(e, d.Entrypoint)
	writeSeq(e, d.Cmd)

	return buf.Bytes()
}

func writeSeq(e *ir.Encoder, xs []string) {
	e.Count(len(xs))

	for _, x := range xs {
		e.Str(x)
	}
}

// ID is id(γ) ≡ ℋ(𝒮(γ)) (3.8).
func ID(d Declaration) ir.NodeID {
	h := ir.NewHasher()

	h.Fixed(Encode(d))

	return h.Sum()
}

// Decode reads what Encode wrote.
func Decode(b []byte) (Declaration, error) {
	d := decoder{b: b}

	if got := string(d.fixed(len(magic))); d.err == nil && got != magic {
		return Declaration{}, fmt.Errorf("not a declaration (magic %q)", got)
	}

	out := Declaration{Env: d.seq()}
	out.WorkingDir = d.str()
	out.User = d.str()
	out.Entrypoint = d.seq()
	out.Cmd = d.seq()

	if d.err != nil {
		return Declaration{}, d.err
	}

	return out, nil
}

// maxSeq bounds a sequence a decoder will allocate for.
//
// A declaration comes off a wire or out of a store, so a length is a claim until
// it is read. Generous against anything an image really declares and small
// enough that a lie costs nothing.
const maxSeq = 1 << 16

// decoder reads the encoding above, carrying its first error rather than
// returning one per call - the same shape the layer reader uses, and for the
// same reason: a decode is a sequence of reads that all have to succeed, and
// checking each one at the call site buries the shape of the record.
type decoder struct {
	err error
	b   []byte
}

func (d *decoder) fixed(n int) []byte {
	if d.err != nil {
		return nil
	}

	if n < 0 || n > len(d.b) {
		d.err = fmt.Errorf("a declaration ends mid-field: %d bytes wanted, %d left", n, len(d.b))

		return nil
	}

	out := d.b[:n]
	d.b = d.b[n:]

	return out
}

func (d *decoder) count() int {
	b := d.fixed(4)
	if d.err != nil {
		return 0
	}

	n := int(uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]))
	if n > maxSeq {
		d.err = fmt.Errorf("a declaration claims %d elements, more than %d", n, maxSeq)

		return 0
	}

	return n
}

func (d *decoder) str() string { return string(d.fixed(d.count())) }

func (d *decoder) seq() []string {
	n := d.count()
	if d.err != nil || n == 0 {
		return nil
	}

	out := make([]string, 0, n)
	for range n {
		out = append(out, d.str())
	}

	if d.err != nil {
		return nil
	}

	return out
}
