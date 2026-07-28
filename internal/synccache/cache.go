// Package synccache implements a concurrent-safe unbounded cache that evaluates and
// stores the result of a value loader.
package synccache

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrAlreadyExists is returned by [Cache.Store] when a key is already present in the cache.
var ErrAlreadyExists = errors.New("already exists")

// Loader is a func that is used to load a cache value.
type Loader[V any] func(ctx context.Context) (V, error)

// entry is a cached value, which may be computed in a background thread.
//
// Lifecycle invariant: value and err are written exactly once, by whoever owns
// loading (the load goroutine, or [Cache.Store] for a pre-built value), and always
// before close(loaded). Readers reach them only after observing a receive from
// loaded (via select or <-loaded), which establishes the happens-before edge.
// Nothing writes value or err after that point, so no lock is needed on the read path.
type entry[V any] struct {
	value    V
	inflight *inflight
	loaded   chan struct{}
	err      error
	mu       sync.Mutex
}

func (e *entry[V]) getInflight() *inflight {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.inflight
}

func (e *entry[V]) clearInflight() {
	e.mu.Lock()
	e.inflight = nil
	e.mu.Unlock()
}

func (e *entry[V]) await(ctx context.Context) (V, error) {
	select {
	case <-e.loaded:
		return e.value, e.err
	case <-ctx.Done():
		select {
		case <-e.loaded:
			return e.value, e.err
		default:
			var zero V
			return zero, ctx.Err()
		}
	}
}

// Cache is an object which can be used to create singletons stored in a key-value store.
type Cache[K comparable, V any] struct {
	store map[K]*entry[V]
	mu    sync.RWMutex
}

// NewCache creates an empty unbounded [Cache].
func NewCache[K comparable, V any]() *Cache[K, V] {
	return &Cache[K, V]{
		store: make(map[K]*entry[V]),
	}
}

// Load executes loader for key, if a value hasn't already been loaded.
//
// Loader is shared: concurrent callers for the same key wait on a single
// loader call, and the loader survives any individual caller's cancellation —
// it is abandoned only once every caller sharing it has gone away. A caller whose own
// context is still live is never handed someone else's cancellation; it starts a fresh
// loader instead.
func (c *Cache[K, V]) Load(ctx context.Context, key K, loader Loader[V]) (V, error) {
	var zero V

	if loader == nil {
		// Not merely unhelpful: load runs on a goroutine, so calling a nil
		// loader would be an unrecoverable panic rather than an error the caller
		// could handle. Reject deterministically, whether or not the key happens to be
		// cached right now.
		return zero, fmt.Errorf("cache: load key %v: loader is required", key)
	}

	for {
		e, execCtx, found := c.getEntry(ctx, key)
		if !found {
			// We need to load this.
			go c.load(execCtx, e, key, loader)
		} else if inf := e.getInflight(); inf != nil {
			// Attach, so the load outlives any single caller — unless it is already
			// doomed, in which case add refuses us. Every context sharing a doomed load
			// has been canceled, so it will fail with a context error and evict itself;
			// attaching would hand our still-live caller someone else's cancellation.
			_ = inf.add(ctx)
		}

		val, err := e.await(ctx)
		if err != nil {
			ctxErr := ctx.Err()
			if ctxErr != nil {
				return zero, fmt.Errorf("cache: load key %v: %w", key, ctxErr)
			}

			// Our own context is live, so a context failure is someone else's — either a
			// doomed load we declined to attach to, or one abandoned in the window
			// between getEntry and getInflight above. Either way it has evicted itself,
			// so a retry starts a fresh load rather than spinning on the same entry.
			if found && isContextErr(err) {
				continue
			}
		}

		return val, err
	}
}

// isContextErr reports whether err is a load failing because its callers went away or ran
// out of time, rather than because the key itself is unloadable.
func isContextErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (c *Cache[K, V]) load(execCtx context.Context, e *entry[V], key K, loader Loader[V]) {
	inf := e.getInflight()
	returned := false

	// Unwinding must publish something, whatever happens: a loader that leaves without
	// returning — a panic, or a runtime.Goexit from a testing helper — would otherwise
	// leave loaded unclosed and every waiter on it blocked for good.
	defer func() {
		if !returned && e.err == nil {
			e.err = fmt.Errorf("cache: load key %v: loader exited without returning a value", key)
		}

		close(e.loaded)
		e.clearInflight()

		// Close unregisters listeners and releases the shared load context.
		if inf != nil {
			inf.close()
		}
	}()

	e.value, e.err = loader(execCtx)
	returned = true

	// Don't cache a context failure: it says the callers went away or ran out of time,
	// not that this key is unloadable. Whoever is currently waiting will still get this,
	// but no future callers to Load will.
	if isContextErr(e.err) {
		c.deleteEntry(key)
	}

	if e.err != nil {
		e.err = fmt.Errorf("cache: load key %v: %w", key, e.err)
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

func (c *Cache[K, V]) getEntry(ctx context.Context, key K) (*entry[V], context.Context, bool) {
	c.mu.RLock()
	e, ok := c.store[key]
	c.mu.RUnlock()

	if ok {
		return e, nil, true
	}

	c.mu.Lock()

	var execCtx context.Context

	e, ok = c.store[key]
	if !ok {
		var inf *inflight

		execCtx, inf = newInflight(ctx)
		e = &entry[V]{
			inflight: inf,
			loaded:   make(chan struct{}),
		}
		c.store[key] = e
	}

	c.mu.Unlock()

	return e, execCtx, ok
}

// deleteEntry removes the entry for key. Note: this does not cancel any ongoing
// load.
//
// INVARIANT (single-deleter): deleting by key rather than by entry identity is safe only
// because an entry can never be evicted by anyone other than the load goroutine that
// owns it. Concretely:
//
//   - [Cache.load] is the only caller, and calls this at most once, immediately after its
//     loader returns;
//   - an entry is present in the store for the whole of its load, and both
//     [Cache.getEntry] and [Cache.Store] refuse to insert over a present entry;
//   - therefore no replacement entry for that key can exist until this delete has already
//     happened, and a stale load cannot evict a newer entry.
//
// Any second deletion path — eviction, Clear, a TTL, a bounded variant of this cache —
// breaks the third bullet, at which point this must become an identity check:
//
//	if c.store[key] == e { delete(c.store, key) }
//
// [TestCache_Store] pins the invariant.
func (c *Cache[K, V]) deleteEntry(key K) {
	c.mu.Lock()
	delete(c.store, key)
	c.mu.Unlock()
}
