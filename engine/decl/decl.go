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
	"strings"

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

	got := string(d.fixed(len(magic), "magic"))
	if d.err == nil && got != magic {
		return Declaration{}, fmt.Errorf("not a declaration: want magic %q, found %q%s",
			magic, shown(b), whatThatIs(got))
	}

	out := Declaration{Env: d.seq("Env")}
	out.WorkingDir = d.str("WorkingDir")
	out.User = d.str("User")
	out.Entrypoint = d.seq("Entrypoint")
	out.Cmd = d.seq("Cmd")

	if d.err != nil {
		return Declaration{}, d.err
	}

	return out, nil
}

// whatThatIs names the stream somebody actually has, where this engine can
// recognise it.
//
// "not a declaration" is true and useless: the reader has bytes from somewhere
// and needs to know which of this engine's encodings they hold. A layer pack
// handed to a declaration decoder is a wiring mistake with an obvious fix, and
// it reads exactly like corruption unless the refusal says otherwise.
// shown is what to print for a stream that is not this one.
//
// Not the bytes compared, which are exactly as many as this format's magic and
// so cut another format's in half - `EBLAYER1` reads as `EBLAYER`, and a reader
// checking it against the layer format finds it matches nothing. A few bytes
// either way costs nothing and the confusion is real.
func shown(b []byte) string {
	const most = 8

	if len(b) > most {
		b = b[:most]
	}

	return string(b)
}

func whatThatIs(got string) string {
	if strings.HasPrefix(got, "EBLAYER") {
		return " - that is a layer pack, which carries a tree rather than what an image declares"
	}

	return ""
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
	// at is how many bytes have been consumed, so a refusal can say where it
	// stopped. A length and a remainder say that something is short; the offset
	// and the field name say *what* is short, which is where somebody looks.
	at int
}

func (d *decoder) fixed(n int, field string) []byte {
	if d.err != nil {
		return nil
	}

	if n < 0 || n > len(d.b) {
		d.err = fmt.Errorf("declaration ends early: %s wanted %d bytes at offset %d, %d remain",
			field, n, d.at, len(d.b))

		return nil
	}

	out := d.b[:n]
	d.b = d.b[n:]
	d.at += n

	return out
}

func (d *decoder) count(field string) int {
	b := d.fixed(4, field+" length")
	if d.err != nil {
		return 0
	}

	n := int(uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]))
	if n > maxSeq {
		d.err = fmt.Errorf("declaration claims %s holds %d entries at offset %d, more than the %d"+
			" this decoder will allocate for", field, n, d.at-4, maxSeq)

		return 0
	}

	return n
}

func (d *decoder) str(field string) string { return string(d.fixed(d.count(field), field)) }

func (d *decoder) seq(field string) []string {
	n := d.count(field)
	if d.err != nil || n == 0 {
		return nil
	}

	out := make([]string, 0, n)

	for i := range n {
		out = append(out, d.str(fmt.Sprintf("%s[%d]", field, i)))
	}

	if d.err != nil {
		return nil
	}

	return out
}
