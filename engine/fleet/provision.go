package fleet

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Keeper is the part of a blob store a worker fills.
//
// Shaped after `blob.Store`, and one detail of that shape is load-bearing:
// **Put names the blob, the caller does not**. A store that let a caller choose
// the name could file bytes under the digest that was wanted rather than the one
// that arrived, which is the single failure that would let a wrong layer be
// keyed as a right one.
type Keeper interface {
	Has(id ir.NodeID) bool
	Put(r io.Reader) (ir.NodeID, int64, error)
}

// Provision makes sure this machine holds an assignment's inputs.
//
// The piece the fleet was missing. `Runner` hands an assignment's digests to the
// executor, which materialises from **this machine's** store: a worker that had
// never seen the base could not run the step at all, and the end-to-end test
// only passed because its blob source claimed to hold everything (E258).
//
// Three properties, and the middle one is the whole argument for a fleet:
//
//   - what is missing is fetched, verified per chunk on the way in (C.4, E238);
//   - **what is present is not fetched**. A base layer is hundreds of megabytes
//     and a worker keeps its store between steps, so refetching per step would
//     spend more time on the network than the steps spend building - which is
//     how a distributed build ends up slower than one machine;
//   - a blob nobody can supply is a refusal. Running the step anyway would key it
//     as though it had the input (I3), and cache a wrong answer for everybody.
//
// Sources are tried in the order given, which is C.4's: peers said to hold it,
// then the rest, then the origin.
func Provision(
	ctx context.Context, into Keeper, a Assignment, from ...Source,
) (Transfer, error) {
	missing := lacking(into, a)
	if len(missing) == 0 {
		// Nothing to say and nobody to say it to. The common case once a fleet
		// is warm, and a connection opened to be told so is a connection that
		// costs more than it saves.
		//
		// A zero Transfer, which is the point: a warm worker and a cold one have
		// to be distinguishable in the account, or a fleet that spends all its
		// time moving bytes looks exactly like one that does not (E259).
		return Transfer{}, nil
	}

	began := time.Now()

	// Its own loop over C.4's order rather than `Fetch.Get`, because a layer is
	// identified by *storing* it: the check is "unpack this and capture what
	// comes out", which is exactly what `Keeper.Put` does. Buffering first and
	// storing after would mean unpacking twice - once to find out what arrived
	// and once to keep it - on the one path this whole exercise exists to make
	// fast.
	//
	// The ordering matters as much as the check. A source that answers with the
	// wrong layer is **skipped and the next tried** (I6, E263): no single source
	// is load-bearing, so a peer with a rotted disk costs a retry rather than a
	// build. Getting this wrong is easy and quiet - the first version verified
	// after the loop, which turned a lying holder into a failure.
	f := &Fetch{Peers: from}

	moved := Transfer{}
	want := missing

	// Why the last source could not answer.
	//
	// A source that cannot answer is not a failure - that is what having several
	// is for (I6) - but when *none* of them could, the reasons are all there was
	// to learn and every one was being thrown away. "Some blobs could not be
	// fetched" is a count without a cause, and it is what an afternoon of
	// two-machine runs produced (E308, E309).
	//
	// The last, not all: a fleet of twenty workers would give twenty lines of
	// one timeout, and the useful case is one or two sources with one real
	// reason between them.
	var last error

	// Who was asked and had nothing.
	//
	// **A source that answered "none" and a source that was never reached read
	// the same** without this, and they need opposite fixes: one is a store that
	// should have held it, the other is an address, a firewall or a hint. Five
	// two-machine experiments went into telling those two apart by hand (E312).
	//
	// Every one of them, not just the last: a failing source's reason is the
	// more interesting thing and still leads the message, but "nothing failed"
	// is precisely the case where the list of who was consulted is all there is.
	var empty []string

	for _, src := range f.order() {
		if len(want) == 0 {
			break
		}

		before := len(want)

		got, err := src.Fetch(ctx, want)
		if err != nil {
			last = fmt.Errorf("%s: %w", src.Name(), err)

			continue
		}

		var still []ir.NodeID

		for _, id := range want {
			r, ok := got[id]
			if !ok {
				still = append(still, id)

				continue
			}

			n, err := keep(into, id, r)
			if err != nil {
				// Not what it claimed to be, or would not store. Somebody else
				// may have it (I6), so the loop goes on - but **the reason is
				// kept**.
				//
				// It was not, and that is the sentence that ended E312: a layer
				// arrived, captured under a different digest, and this end
				// reported that the peer did not hold it. `keep` writes "asked
				// for X and got Y" one line above, which says exactly what
				// happened, and it was being discarded.
				last = fmt.Errorf("%s sent it: %w", src.Name(), err)

				still = append(still, id)

				continue
			}

			moved.Bytes += n
		}

		if len(still) == before {
			// Reached, and had none of them. Named *after* the fetch, because a
			// source that supplied some of what was wanted is a different thing
			// again and saying it "had nothing" would be false.
			empty = append(empty, src.Name())
		}

		want = still
	}

	moved.Took = time.Since(began)

	if len(want) > 0 {
		if last != nil {
			return moved, fmt.Errorf("%d of %d input(s) for a delegated step: %w"+
				"\n  first %v, and the last source said: %w",
				len(want), len(missing), ErrNotFetched, want[0], last)
		}

		if len(empty) > 0 {
			return moved, fmt.Errorf("%d of %d input(s) for a delegated step: %w"+
				"\n  first %v, and it was not held by: %s", len(want),
				len(missing), ErrNotFetched, want[0], strings.Join(empty, ", "))
		}

		// Nothing failed and nobody was asked. The state E312 spent five
		// experiments in, and the one this message used to be silent about.
		return moved, fmt.Errorf("%d of %d input(s) for a delegated step: %w"+
			"\n  first %v, and no source was consulted at all", len(want),
			len(missing), ErrNotFetched, want[0])
	}

	return moved, nil
}

// keep stores one arrival, refusing it if it is not what was asked for.
//
// **The store names it, and the name is checked.** A store that filed what
// arrived under the digest that was asked for would serve corruption for ever
// after, and every key derived from that base would name something else (§5.3).
func keep(into Keeper, id ir.NodeID, r io.Reader) (int64, error) {
	filed, n, err := into.Put(r)
	if err != nil {
		return 0, fmt.Errorf("keep %v: %w", id, err)
	}

	if filed != id {
		return 0, fmt.Errorf("%w: asked for %v and got %v", ErrNotALayer, id, filed)
	}

	return n, nil
}

// Transfer is what provisioning had to move.
//
// Counted at the store rather than at the wire: what matters to the account is
// the bytes this step needed that this machine did not have, not the framing
// around them. The two differ by the verification tree, which is a fixed
// fraction and not the thing anybody would change a build to avoid.
type Transfer struct {
	Bytes int64
	Took  time.Duration
}

// lacking is every input this machine does not already hold.
//
// The "once each" is `standsOn`'s job, which is the one place that says what an
// assignment reads - a second walk of the same two fields is a second thing to
// get out of step with the first.
func lacking(into Keeper, a Assignment) []ir.NodeID {
	var out []ir.NodeID

	for _, id := range standsOn(a) {
		if !into.Has(id) {
			out = append(out, id)
		}
	}

	return out
}
