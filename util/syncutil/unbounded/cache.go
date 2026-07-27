// Package unbounded implements a concurrent-safe unbounded cache that evaluates and
// stores the result of a functional constructor.
package unbounded

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/EarthBuild/earthbuild/util/syncutil/metacontext"
)

// Constructor is a func that is used to construct a cache value, given a key.
type Constructor[K comparable, V any] func(ctx context.Context, key K) (V, error)

// entry is a cached value, which may be computed in a background thread.
//
// Lifecycle invariant: value and err are written exactly once, by whoever owns
// construction (the construct goroutine, or Add for a pre-built value), and always
// before both done.Store(true) and close(constructed). Readers reach them only after
// observing done == true or a receive from constructed, either of which establishes the
// happens-before edge. Nothing writes value or err after that point, so no lock is
// needed on the read path.
type entry[V any] struct {
	metaCtx     atomic.Pointer[metacontext.MetaContext]
	constructed chan struct{}
	err         error
	value       V

	// done indicates whether construction is complete, enabling zero-allocation fast-path hits.
	done atomic.Bool
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

// Do executes the constructor, if a value for key hasn't already been constructed.
//
// Construction is shared: concurrent callers for the same key wait on a single
// constructor call, and the construction survives any individual caller's cancellation —
// it is abandoned only once every caller sharing it has gone away. A caller whose own
// context is still live is never handed someone else's cancellation; it starts a fresh
// construction instead.
func (c *Cache[K, V]) Do(ctx context.Context, key K, constructor Constructor[K, V]) (V, error) {
	var zero V

	if constructor == nil {
		// Not merely unhelpful: construct runs on a goroutine, so calling a nil
		// constructor would be an unrecoverable panic rather than an error the caller
		// could handle. Reject deterministically, whether or not the key happens to be
		// cached right now.
		return zero, errors.New("nil constructor")
	}

	for retry := false; ; retry = true {
		// Only on a retry: a cache hit is still served to a caller whose context has since
		// been canceled, as it always was. But there is no point starting construction
		// over on behalf of a caller that has itself gone away, and this is what bounds
		// the loop.
		if retry {
			if err := ctx.Err(); err != nil {
				return zero, err
			}
		}

		e, found := c.getEntry(ctx, key)
		if !found {
			// We need to construct this.
			go c.construct(e, key, constructor)

			<-e.constructed

			return e.value, e.err
		}

		if e.done.Load() {
			return e.value, e.err
		}

		select {
		case <-e.constructed:
			// Already constructed — fast path!
			return e.value, e.err
		default:
		}

		if mc := e.metaCtx.Load(); mc != nil && mc.Add(ctx) != nil {
			// The in-flight construction is already doomed — every context sharing it
			// has been canceled, so it will fail with context.Canceled and evict itself.
			// Attaching would hand our still-live caller someone else's cancellation.
			// Wait for it to clear the way, then start over.
			<-e.constructed

			continue
		}

		<-e.constructed

		return e.value, e.err
	}
}

func (c *Cache[K, V]) construct(e *entry[V], key K, constructor Constructor[K, V]) {
	// The metaCtx will ensure that this stays alive even if the original Do has
	// been canceled, thanks to the metaCtx. This is canceled only when ALL of
	// the Do's are canceled.
	mc := e.metaCtx.Load()
	e.value, e.err = constructor(mc, key)
	// Don't cache context canceled. Whoever is currently waiting will still get this,
	// but no future callers to Do will.
	if errors.Is(e.err, context.Canceled) {
		c.deleteEntry(key)
	}

	e.done.Store(true)
	close(e.constructed)
	e.metaCtx.Store(nil)

	// Dropping the pointer above is not enough: the MetaContext's own monitor and watcher
	// goroutines hold it, and block until every sub-context is canceled — which for a
	// whole-build context is never. Close releases them, and with them the sub-contexts.
	mc.Close()
}

// Add adds a readily constructed value for a given key.
func (c *Cache[K, V]) Add(key K, value V) error {
	c.mu.Lock()

	if _, ok := c.store[key]; ok {
		c.mu.Unlock()

		return errors.New("already exists")
	}

	e := &entry[V]{
		constructed: make(chan struct{}),
		value:       value,
	}
	e.done.Store(true)
	close(e.constructed)

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
			constructed: make(chan struct{}),
		}
		e.metaCtx.Store(metacontext.New(ctx))
		c.store[key] = e
	}

	c.mu.Unlock()

	return e, ok
}

// deleteEntry removes the entry for key. Note: this does not cancel any ongoing
// construction.
//
// INVARIANT (single-deleter): deleting by key rather than by entry identity is safe only
// because an entry can never be evicted by anyone other than the construct goroutine that
// owns it. Concretely:
//
//   - construct is the only caller, and calls this at most once, immediately after its
//     constructor returns;
//   - an entry is present in the store for the whole of its construction, and both
//     getEntry and Add refuse to insert over a present entry;
//   - therefore no replacement entry for that key can exist until this delete has already
//     happened, and a stale construct cannot evict a newer entry.
//
// Any second deletion path — eviction, Clear, a TTL, a bounded variant of this cache —
// breaks the third bullet, at which point this must become an identity check:
//
//	if c.store[key] == e { delete(c.store, key) }
//
// TestCache_DeleteEntryIdentity pins the invariant.
func (c *Cache[K, V]) deleteEntry(key K) {
	c.mu.Lock()
	delete(c.store, key)
	c.mu.Unlock()
}
