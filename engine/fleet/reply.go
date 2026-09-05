package fleet

import (
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Reply is what a worker sends back: C.3's "result digest, exit code,
// observation set and measured duration".
//
// **Digests, never bytes.** Nothing here carries a payload, and that is a
// property of the type rather than a convention. Blobs move on `earth/blob/1`
// in batches, verified per chunk (C.4, I2), so a peer serving wrong bytes is
// detected within one chunk rather than at the end of a transfer. A result
// inlined into a control message would be a payload nobody chunk-verified,
// arriving on the stream this engine trusts most.
//
// Everything here is a **claim**. The worker ran the step and this is what it
// says happened; the driver fetches the layer by its digest and verifies it, and
// nothing in this message can make it skip that. A5 is the assumption that
// makes the fleet honest, and it is an assumption about the driver's scepticism
// rather than about the worker's good faith.
type Reply struct {
	// Version is the wire vocabulary, first and always present, for the same
	// reason as on an Assignment: a peer must be able to refuse a message
	// before interpreting any of it.
	Version int `json:"version"`
	// Layer is the digest of what the step produced - the identity, timestamps
	// included.
	Layer ir.NodeID `json:"layer"`
	// Content is the same result with timestamps excluded, so determinism
	// screening can judge a step on what it produced rather than when it ran.
	Content ir.NodeID `json:"content,omitzero"`
	// Exit is the step's exit code. A non-zero one is a **result**, not a
	// transport failure: the step ran and said no.
	Exit int `json:"exit,omitempty"`
	// Bytes is the size of the result, for the driver's transfer-cost estimates.
	Bytes int64 `json:"bytes,omitempty"`
	// Observation is what the worker says the step looked at.
	//
	// A claim like the rest. It reaches Κ₂ only through the driver's own rules -
	// an observation naming nothing is refused rather than trusted, because it
	// agrees with every base and this one came from a machine the driver did
	// not write (A5).
	Observation Observation `json:"observation,omitzero"`
	// DurationMillis is how long the step took, as the worker measured it.
	// Advisory: it feeds scheduling estimates and nothing else (I5).
	DurationMillis int64 `json:"durationMillis,omitempty"`
	// FetchedBytes and FetchMillis are what this worker had to move before it
	// could start, and how long that took.
	//
	// Advisory, and counted rather than believed: they are added to an account
	// and never reach a key or a result, so a worker cannot alter anybody's
	// build by lying about them - only mislead a person reading a report (A5).
	//
	// Reported separately from DurationMillis because a fleet that is no faster
	// than one machine needs to say *which*: moving inputs, waiting, or the step
	// itself. Those have three different remedies (E259).
	// QueueMillis is how long the step waited for a slot on this worker.
	//
	// **A queue is not waste and network time is.** The driver computes overhead
	// by subtracting what a worker reports from the round trip, so a wait that
	// is neither transfer nor step lands in the same number as the wire - and an
	// account that cannot tell a busy fleet from a slow one cannot say whether
	// adding machines would help (E336).
	QueueMillis int64 `json:"queueMillis,omitempty"`
	// FetchedBytes and FetchMillis are what this worker had to move.
	FetchedBytes int64 `json:"fetchedBytes,omitempty"`
	FetchMillis  int64 `json:"fetchMillis,omitempty"`
	// Platform is what this worker is, as `ir.Platform.String` writes it.
	//
	// **The driver cannot derive it**: a worker is the only party that knows
	// what it can execute. Placement refuses a worker whose platform it does not
	// know (E267), so a fleet that never announced itself would be a fleet that
	// never receives a step - and the build would look local while the machines
	// idled.
	Platform string `json:"platform,omitempty"`
	// Capacity is how many steps this worker can run at once.
	//
	// Advisory, and the denominator the driver has no other way to learn. It
	// balances on how *full* a machine is rather than on how many steps it is
	// running, because otherwise a sixty-four core machine and a four core one
	// get an equal share and the build finishes when the small one does (E272).
	Capacity int `json:"capacity,omitempty"`
	// HeldAt is where this worker can be reached for what it just produced.
	//
	// **Advisory, and self-announced.** The worker knows its own address; the
	// driver may not - a worker behind NAT has no address the driver could
	// derive. So it says, and the driver passes it on to whoever needs that
	// layer next (E260).
	//
	// Safe to accept unchecked because it names somewhere to *try*: every byte
	// fetched from it is verified against the digest that was asked for (C.4,
	// E238), so a wrong or malicious address costs a retry against the next
	// source and can never produce a wrong build. Empty is ordinary - a fleet
	// sharing one store has nothing to serve.
	HeldAt string `json:"heldAt,omitempty"`
	// Refused says the worker would not run the step, and why.
	//
	// Distinct from a non-zero exit, which is a step that ran. A delegate is an
	// engine and refuses what any engine refuses - a `host` op it cannot
	// express, a construct it does not implement - and saying so is what lets
	// the driver run it somewhere else rather than fail the build (I10).
	Refused string `json:"refused,omitempty"`
}

// Observation is a worker's account of what a step looked at.
//
// The wire's own type, not `core.Observation`, on the same argument as `Op`: the
// two are free to differ, and a field added to the engine's internal
// representation should not silently become something peers exchange.
type Observation struct {
	// Reads are paths read, with the digest of what was read.
	Reads map[string]ir.NodeID `json:"reads,omitempty"`
	// Negative are lookups that found nothing.
	Negative []string `json:"negative,omitempty"`
	// Listings are directories enumerated, with the digest of each listing.
	Listings map[string]ir.NodeID `json:"listings,omitempty"`
	// Incomplete says the worker knows it missed something. A worker that
	// reports this honestly costs itself an L2 hit, which is why the field is
	// worth having: the alternative is a claim the driver cannot check.
	Incomplete bool `json:"incomplete,omitempty"`
}
