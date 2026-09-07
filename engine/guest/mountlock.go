package guest

import (
	"slices"
	"sync"
)

// mountLocks serialises steps that share a cache.
//
// `CACHE --sharing=locked` is the default and the only mode this engine accepts,
// and it was not being provided: the guest's only lock is per *handle*, so two
// steps naming one cache id used it at the same time (E427). An option accepted
// and not provided is the failure this project refuses everywhere else.
//
// Per id rather than one lock over all mounts: steps using unrelated caches must
// not wait for each other, which would be a real cost paid for nothing.
//
// Secrets are excluded. Each step gets its own staged copy of a credential, so
// there is nothing shared to queue on, and queueing on the name would serialise
// a build over a resource that does not exist.
type mountLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// hold takes every cache a step needs and returns their release.
//
// **Sorted, and that is the whole of the deadlock argument.** A step wanting
// {a,b} and one wanting {b,a} would otherwise each hold what the other needs;
// acquiring in a fixed order makes the cycle unconstructable rather than
// unlikely, and does not depend on the order an Earthfile happens to declare
// them in.
func (l *mountLocks) hold(mounts []Mount) func() {
	ids := LockOrder(mounts)

	held := make([]*sync.Mutex, 0, len(ids))

	for _, id := range ids {
		m := l.lockFor(id)
		m.Lock()

		held = append(held, m)
	}

	return func() {
		// Released in reverse, which costs nothing and keeps the pairing
		// obvious to anybody reading it beside the acquisition above.
		for i := range slices.Backward(held) {
			held[i].Unlock()
		}
	}
}

// LockOrder is the cache ids a step holds while it runs.
//
// Exported because the scheduler computes the same set over `ir.Mount` before it
// dispatches the step, and two lists over one rule are kept in step by a test
// rather than by hope (E434).
//
// Which caches a step waits on, in the order it takes them.
//
// Separated from the taking so the order can be asserted directly. A test that
// only tries to provoke a deadlock proves nothing when it passes - the mutation
// sweep deleted the sort and two goroutines racing fifty times each failed to
// notice, because a race not observed is not a race disproved (E427).
func LockOrder(mounts []Mount) []string {
	ids := make([]string, 0, len(mounts))

	for _, m := range mounts {
		// A secret is staged per step, and a `--sharing=shared` cache is one the
		// author said several steps may use at once - so neither is queued on.
		if m.ID == "" || m.Secret != "" || !m.Exclusive {
			continue
		}

		if !slices.Contains(ids, m.ID) {
			ids = append(ids, m.ID)
		}
	}

	// Sorted, and that is the whole of the deadlock argument: a step wanting
	// {a,b} and one wanting {b,a} would otherwise each hold what the other
	// needs. Ordering makes the cycle unconstructable rather than unlikely.
	slices.Sort(ids)

	return ids
}

// lockFor is the lock for one cache id, made on first use.
func (l *mountLocks) lockFor(id string) *sync.Mutex {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.locks == nil {
		l.locks = map[string]*sync.Mutex{}
	}

	if m, ok := l.locks[id]; ok {
		return m
	}

	m := &sync.Mutex{}
	l.locks[id] = m

	return m
}
