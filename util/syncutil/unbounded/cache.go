// Package unbounded implements a concurrent-safe unbounded cache that evaluates and
// stores the result of a value loader.
package unbounded

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/EarthBuild/earthbuild/util/syncutil/metacontext"
)

// ErrAlreadyExists is returned by [Cache.Store] when a key is already present in the cache.
var ErrAlreadyExists = errors.New("already exists")

// Loader is a func that is used to load a cache value for a given key.
type Loader[K comparable, V any] func(ctx context.Context, key K) (V, error)

// entry is a cached value, which may be computed in a background thread.
//
// Lifecycle invariant: value and err are written exactly once, by whoever owns
// loading (the load goroutine, or Store for a pre-built value), and always
// before close(loaded). Readers reach them only after observing a receive from
// loaded (via select or <-loaded), which establishes the happens-before edge.
// Nothing writes value or err after that point, so no lock is needed on the read path.
type entry[V any] struct {
	value   V
	metaCtx *metacontext.MetaContext
	loaded  chan struct{}
	err     error
	mu      sync.Mutex
}

func (e *entry[V]) getMetaCtx() *metacontext.MetaContext {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.metaCtx
}

func (e *entry[V]) clearMetaCtx() {
	e.mu.Lock()
	e.metaCtx = nil
	e.mu.Unlock()
}

// Cache is an object which can be used to create singletons stored in a key-value store.
type Cache[K comparable, V any] struct {
	store  map[K]*entry[V]
	loader Loader[K, V]
	mu     sync.RWMutex
}

// NewCache creates an empty unbounded [Cache]. An optional default loader function
// may be provided.
func NewCache[K comparable, V any](loader ...Loader[K, V]) *Cache[K, V] {
	c := &Cache[K, V]{
		store: make(map[K]*entry[V]),
	}

	if len(loader) > 0 {
		c.loader = loader[0]
	}

	return c
}

// Load executes the loader, if a value for key hasn't already been loaded.
//
// Loader is shared: concurrent callers for the same key wait on a single
// loader call, and the loader survives any individual caller's cancellation —
// it is abandoned only once every caller sharing it has gone away. A caller whose own
// context is still live is never handed someone else's cancellation; it starts a fresh
// loader instead.
func (c *Cache[K, V]) Load(ctx context.Context, key K, loader ...Loader[K, V]) (V, error) {
	var zero V

	l := c.loader
	if len(loader) > 0 && loader[0] != nil {
		l = loader[0]
	}

	if l == nil {
		// Not merely unhelpful: load runs on a goroutine, so calling a nil
		// loader would be an unrecoverable panic rather than an error the caller
		// could handle. Reject deterministically, whether or not the key happens to be
		// cached right now.
		return zero, fmt.Errorf("cache: load key %v: loader is required", key)
	}

	for {
		e, found := c.getEntry(ctx, key)
		if !found {
			// We need to load this.
			go c.load(e, key, l)
		} else {
			select {
			case <-e.loaded:
				return e.value, e.err
			default:
				mc := e.getMetaCtx()
				if mc != nil && mc.Add(ctx) != nil {
					// The in-flight load is already doomed — every context sharing it
					// has been canceled, so it will fail with context.Canceled and evict itself.
					// Attaching would hand our still-live caller someone else's cancellation.
					// Wait for it to clear the way, then check context status before retrying.
					<-e.loaded

					err := ctx.Err()
					if err != nil {
						return zero, fmt.Errorf("cache: load key %v: %w", key, err)
					}

					continue
				}
			}
		}

		<-e.loaded

		return e.value, e.err
	}
}

func (c *Cache[K, V]) load(e *entry[V], key K, loader Loader[K, V]) {
	// The metaCtx will ensure that this stays alive even if the original Load has
	// been canceled, thanks to the metaCtx. This is canceled only when ALL of
	// the Loads are canceled.
	mc := e.getMetaCtx()
	e.value, e.err = loader(mc, key)
	// Don't cache context canceled. Whoever is currently waiting will still get this,
	// but no future callers to Load will.
	if errors.Is(e.err, context.Canceled) {
		c.deleteEntry(key)
	}

	if e.err != nil {
		e.err = fmt.Errorf("cache: load key %v: %w", key, e.err)
	}

	close(e.loaded)
	e.clearMetaCtx()

	// Dropping the pointer above is not enough: the MetaContext's own monitor and watcher
	// goroutines hold it, and block until every sub-context is canceled — which for a
	// whole-build context is never. Close releases them, and with them the sub-contexts.
	if mc != nil {
		mc.Close()
	}
}

// Store stores a value for a given key.
func (c *Cache[K, V]) Store(key K, value V) error {
	c.mu.Lock()

	if _, ok := c.store[key]; ok {
		c.mu.Unlock()

		return fmt.Errorf("cache: store key %v: %w", key, ErrAlreadyExists)
	}

	e := &entry[V]{
		loaded: make(chan struct{}),
		value:  value,
	}
	close(e.loaded)

	c.store[key] = e
	c.mu.Unlock()

	return nil
}

func (c *Cache[K, V]) getEntry(ctx context.Context, key K) (*entry[V], bool) {
	c.mu.RLock()
	e, ok := c.store[key]
	c.mu.RUnlock()

	if ok {
		return e, true
	}

	c.mu.Lock()

	e, ok = c.store[key]
	if !ok {
		e = &entry[V]{
			metaCtx: metacontext.New(ctx),
			loaded:  make(chan struct{}),
		}
		c.store[key] = e
	}

	c.mu.Unlock()

	return e, ok
}

// deleteEntry removes the entry for key. Note: this does not cancel any ongoing
// load.
//
// INVARIANT (single-deleter): deleting by key rather than by entry identity is safe only
// because an entry can never be evicted by anyone other than the load goroutine that
// owns it. Concretely:
//
//   - load is the only caller, and calls this at most once, immediately after its
//     loader returns;
//   - an entry is present in the store for the whole of its load, and both
//     getEntry and Store refuse to insert over a present entry;
//   - therefore no replacement entry for that key can exist until this delete has already
//     happened, and a stale load cannot evict a newer entry.
//
// Any second deletion path — eviction, Clear, a TTL, a bounded variant of this cache —
// breaks the third bullet, at which point this must become an identity check:
//
//	if c.store[key] == e { delete(c.store, key) }
//
// TestCache_Store pins the invariant.
func (c *Cache[K, V]) deleteEntry(key K) {
	c.mu.Lock()
	delete(c.store, key)
	c.mu.Unlock()
}
