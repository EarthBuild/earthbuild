package core

import (
	"slices"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// claims serialise the steps that share a `--sharing=locked` cache.
//
// The guest holds a lock over the directory it binds, which is where the
// guarantee belongs: it is the thing doing the binding, and it is what a step
// run by any other route still meets. This is a different obligation - **where
// a step waits**. The guest waits with a build slot in hand, so three steps
// queueing on one cache idle three slots, and the steps that would have used
// them are the ones with no cache at all (E434).
//
// So the two are not one rule written twice. One decides who may be in the
// directory; this decides who may be *dispatched*. That they agree about which
// mounts are involved is asserted rather than assumed, by a test that walks both.
type claims struct {
	mu   sync.Mutex
	held map[string]chan struct{}
}

// take claims every locked cache the step needs, in a fixed order.
//
// Sorted, because two steps naming caches `a` and `b` in opposite orders would
// otherwise take them in opposite orders and wait for each other for ever. That
// is the deadlock the guest's `lockOrder` already avoids, and it does not stop
// being possible one layer up.
//
// The returned function releases them. It is never nil, so the caller's `defer`
// needs no condition - a release that has to be guarded is a release somebody
// eventually forgets.
func (c *claims) take(mounts []ir.Mount) func() {
	ids := ClaimOrder(mounts)

	for _, id := range ids {
		c.one(id)
	}

	return func() {
		for _, id := range ids {
			c.free(id)
		}
	}
}

// ClaimOrder is the cache ids a step must hold before it is dispatched.
//
// Exported because the guest computes the same set over its own mount type, and
// the two are checked against each other rather than trusted to agree (E434).
//
// Only `locked` mounts: `shared` says several steps may use the directory at
// once and `private` names no shared directory at all (§3.3c). Claiming either
// would provide `locked` under a name that asked for something else, which is
// the failure E427 recorded one layer down.
func ClaimOrder(mounts []ir.Mount) []string {
	var ids []string

	for _, m := range mounts {
		if m.ID == "" || m.Secret || !m.Exclusive {
			continue
		}

		ids = append(ids, m.ID)
	}

	slices.Sort(ids)

	return slices.Compact(ids)
}

// one waits until nothing holds this id, then holds it.
func (c *claims) one(id string) {
	for {
		c.mu.Lock()

		if c.held == nil {
			c.held = map[string]chan struct{}{}
		}

		wait, taken := c.held[id]
		if !taken {
			c.held[id] = make(chan struct{})
			c.mu.Unlock()

			return
		}

		c.mu.Unlock()
		// Woken by whoever releases it. A channel rather than a sleep, so this
		// costs nothing while it waits and admits the next step immediately -
		// polling here would show up as a build that is slower than its own
		// serialisation requires.
		<-wait
	}
}

// free releases an id and wakes everything waiting for it.
func (c *claims) free(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	wait, taken := c.held[id]
	if !taken {
		return
	}

	delete(c.held, id)
	close(wait)
}
