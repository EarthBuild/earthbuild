package remote

import (
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Elsewhere answers for a blob this machine does not hold.
//
// **The one thing the cache-sharing design turned out to need.** Everything
// around it was already built: 𝔅 names blobs by their digests and cannot be
// poisoned (§2.1), `fleet.Blobs` already makes a blob store a place a step's
// faults are answered from, and this agent already speaks a protocol other tools
// use. What was missing was a machine's cache being able to say *not here, but I
// know who* - so a worker's client asks its own agent, and the agent asks the
// fleet.
//
// Shaped after `store.DirStore.Node` rather than after anything new, so that a
// store is already one of these and a fleet source becomes one by having the
// method it has.
//
// Nil wherever there is nobody to ask, which is every agent outside a fleet.
type Elsewhere interface {
	// Node is the blob under this digest, or an error meaning "not from me".
	//
	// An error is an ordinary answer and never fatal: the caller turns it into
	// the 404 it would have returned anyway (I4's degrade-to-miss, applied to
	// one more layer of the storage stack).
	Node(id ir.NodeID) ([]byte, error)
}

// fromElsewhere is a blob fetched from somewhere this engine does not control.
//
// **Verified here, because nothing else will.** A blob read out of the local
// store is checked against the name it is filed under (`store.DirStore.Node`),
// and bytes arriving from another machine deserve the same treatment and get it
// nowhere else: §5.3 says cross-domain entries are unauthenticated data until
// verified, and A5 is an assumption about this engine's scepticism rather than
// about a peer's good faith.
//
// A mismatch is a miss rather than an error, which is 𝔅's own rule: a store
// returning wrong bytes is detected on read, the read becomes a miss, and an
// attacker with total control of a peer can deny service and nothing else.
func fromElsewhere(e Elsewhere, id ir.NodeID) ([]byte, bool) {
	if e == nil {
		return nil, false
	}

	b, err := e.Node(id)
	if err != nil {
		return nil, false
	}

	if ir.DigestOf(b) != id {
		return nil, false
	}

	return b, true
}
