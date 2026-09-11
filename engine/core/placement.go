package core

// Placement is where a copy put something.
//
// **Told rather than rediscovered.** Deciding where `COPY --dir crates .` lands
// is `placedAs` and `intoDir` in the guest, reading a working directory, a
// trailing separator and the source's own kind; a second implementation of that
// rule elsewhere is the two-functions-over-one-struct defect this engine keeps a
// key guard for. The copy records what it did and everything downstream reads
// the record.
//
// What reads it: a job-level skip key has to rewrite the paths a step was seen
// to read - which are paths inside the step's filesystem - into paths in the
// checkout it was built from, and the copy is the only thing that knows the
// correspondence (docs-internals/job-skipping.md).
//
// Not part of an Observation, deliberately. An observation is hashed into Κ₂
// (green paper §3.6) and a placement is not a thing a step *read*; folding it in
// would change every observed key for a fact about provenance.
type Placement struct {
	// Layer is the layer the bytes came from, as the protocol spells it.
	//
	// A string rather than an ir.NodeID because a build context is filed under
	// the identity the plan gave it and a step's output under its own digest,
	// and this is whichever of those the copy was pointed at.
	Layer string
	// From is the path within that layer, slash-separated and relative to it.
	//
	// For a context layer this is the path the checkout has, because a context
	// is staged under the path it has in the context (engine/exec,
	// copyContextInto).
	From string
	// To is where it landed in the step's filesystem, absolute and
	// slash-separated.
	To string
}
