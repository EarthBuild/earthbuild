package fleet

import "sync"

// Told is which map describes each cache, as the driver last said.
//
// `Nearby`'s sibling and `Peers`' cousin: set by the runner where an assignment
// is in hand, read by whatever runs during the step, and empty until a driver
// says otherwise. A worker with nothing here fills its caches by doing the work,
// which is what every worker did before.
//
// Named for what it is rather than for what it holds. The engine has a `Map` for
// a cache's keys and a `cachemaps` directory of pointers, and a third thing
// called `CacheMaps` would be the one nobody could tell apart from the other two.
type Told struct {
	mu sync.RWMutex
	at map[string]string
}

// Set replaces what this worker has been told.
func (t *Told) Set(m map[string]string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.at = m
}

// Of is the map digest for a cache, by the key the driver used.
func (t *Told) Of(key string) (string, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	id, ok := t.at[key]

	return id, ok
}
