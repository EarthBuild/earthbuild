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
func (c *Cache[K, V]) Do(ctx context.Context, key K, constructor Constructor[K, V]) (V, error) {
	e, found := c.getEntry(ctx, key)
	if found {
		if e.done.Load() {
			return e.value, e.err
		}

		select {
		case <-e.constructed:
			// Already constructed — fast path!
		default:
			mc := e.metaCtx.Load()
			if mc != nil {
				_ = mc.Add(ctx)
			}
		}
	} else {
		// We need to construct this.
		go c.construct(e, key, constructor)
	}

	<-e.constructed

	return e.value, e.err
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
	e.metaCtx.Store(nil) // Clear metaCtx to allow GC of underlying sub-contexts.
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

func (c *Cache[K, V]) deleteEntry(key K) {
	// note; this does not cancel any ongoing construction.
	c.mu.Lock()
	delete(c.store, key)
	c.mu.Unlock()
}
