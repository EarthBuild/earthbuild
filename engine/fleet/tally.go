package fleet

import (
	"sync/atomic"
	"time"
)

// Tally is what a worker has fetched by faulting.
//
// **One per worker, not one per fault.** A `Filler` is made per path a step
// opens, so a total kept inside one of them is a total of one fault. Shared
// here, it is the number the reply carries and therefore the number a build
// reports - which read `0 B in 0 fetch(es)` while 1.1 GiB crossed a LAN (E-F0).
//
// Read by difference, per step: `Since` is what has been added between two
// snapshots, because a worker's fillers outlive any one assignment and a total
// would charge every step for the whole build.
type Tally struct {
	bytes   atomic.Int64
	fetches atomic.Int64
	nanos   atomic.Int64
}

// add records one fetch.
func (t *Tally) add(bytes int64, took time.Duration) {
	if t == nil {
		return
	}

	t.bytes.Add(bytes)
	t.fetches.Add(1)
	t.nanos.Add(int64(took))
}

// Moved is everything this tally has seen.
func (t *Tally) Moved() Transfer {
	if t == nil {
		return Transfer{}
	}

	return Transfer{Bytes: t.bytes.Load(), Took: time.Duration(t.nanos.Load())}
}

// Fetches is how many round trips those bytes took.
func (t *Tally) Fetches() int64 {
	if t == nil {
		return 0
	}

	return t.fetches.Load()
}

// Since is what has been added since a snapshot, which is one step's share.
func (t *Tally) Since(was Transfer) Transfer {
	now := t.Moved()

	return Transfer{Bytes: now.Bytes - was.Bytes, Took: now.Took - was.Took}
}
