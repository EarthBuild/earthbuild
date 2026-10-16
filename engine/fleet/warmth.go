package fleet

import "sync"

// warmth is which machines have filled which cache mounts.
//
// **The locality this engine could not see.** Placement models where a *layer*
// is (`holdsBase`) and nothing else, so it cannot tell a worker that has built
// with `go-build` before from one whose directory is empty. For the builds a
// fleet exists to speed up that is the larger of the two costs: a cold cache
// means recompiling what the machine beside it already holds, which is work
// rather than transfer, and no amount of layer affinity avoids it (E-F3).
//
// **Inferred, never announced.** A worker that ran a step with cache id `k` made
// the directory and now has it, so the driver learns this from the assignment it
// already sent and the reply it already received. Nothing crosses the wire, no
// field is added to a message, and no worker is asked a question it might answer
// wrongly.
//
// **Held by the placer, not by the fleet.** This is spent in one place - the
// ordering in `Assign` - so it lives beside it rather than in `Delegating`,
// which would have to carry it across the wire as a hint in order to hand it to
// the machine that already knows.
//
// Advice, like every other input to placement, and it can be absent, stale or
// wrong in either direction without changing a result (I5). A machine recorded
// warm that turns out to be cold recompiles, which is what would have happened
// anyway; a warm machine nobody recorded is simply not preferred.
type warmth struct {
	mu sync.Mutex
	at map[string][]string
}

// also records that a machine has now filled these caches.
//
// Ordered by first warmth and deduplicated, for the reason `holders.of` gives:
// a fleet's advice should not vary run to run, and a worker handed the same
// address twice ranks it twice.
//
// An unnamed machine is not recorded. An in-process fleet has no address at all
// and one sharing a store has nothing to be warm *elsewhere* about, and an empty
// string in this table would prefer every machine that could not be named.
func (w *warmth) also(caches []Cache, at string) {
	if at == "" || len(caches) == 0 {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.at == nil {
		w.at = map[string][]string{}
	}

	for _, c := range caches {
		if c.ID == "" {
			continue
		}

		held := w.at[c.ID]

		var seen bool

		for _, was := range held {
			if was == at {
				seen = true

				break
			}
		}

		if !seen {
			w.at[c.ID] = append(held, at)
		}
	}
}

// of is every machine warm for any cache this assignment declares.
//
// Per cache id, because the id is the name two steps agree on and two steps
// naming different ids share nothing. A table that answered for any cache would
// send a step to a machine warm for something else entirely - advice that costs
// a placement and buys nothing.
func (w *warmth) of(a Assignment) []string {
	if len(a.Op.Caches) == 0 {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.at) == 0 {
		return nil
	}

	var (
		out  []string
		seen map[string]bool
	)

	for _, c := range a.Op.Caches {
		for _, host := range w.at[c.ID] {
			if seen[host] {
				continue
			}

			if seen == nil {
				seen = map[string]bool{}
			}

			seen[host] = true

			out = append(out, host)
		}
	}

	return out
}
